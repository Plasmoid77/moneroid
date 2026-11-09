// Package tui is the interactive panel (spec §12). It renders the same Snapshot
// the CLI prints and never owns the miners' lifetime.
package tui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Plasmoid77/moneroid/internal/ansi"
	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/guard"
	"github.com/Plasmoid77/moneroid/internal/node"
	"github.com/Plasmoid77/moneroid/internal/payouts"
	"github.com/Plasmoid77/moneroid/internal/price"
	"github.com/Plasmoid77/moneroid/internal/status"
	"github.com/Plasmoid77/moneroid/internal/systemd"
)

const (
	minWidth, minHeight = 80, 24
	historyLen          = 1200
	historySpan         = 10 * time.Minute
	payoutsEvery        = time.Minute // journal grep for the dashboard summary
	priceEvery          = 10 * time.Minute
	priceRetry          = time.Minute // after a failed request, so a slow start does not hide the rate for 10 minutes
	nodeEvery           = time.Minute // one get_info to P2Pool's Monero node
)

type mode int

const (
	modeDashboard mode = iota
	modeTarget         // choose all|p2pool|xmrig for control or logs
	modeAction         // choose start|stop|restart
	modeConfirm
	modeHelp
	modePayouts
)

var (
	targets = []string{"all", "p2pool", "xmrig"}
	actions = []string{"start", "stop", "restart"}
)

type point struct {
	t time.Time
	v *float64 // nil = gap
}

type Model struct {
	cfg        *config.Config
	collector  *status.Collector
	run        systemd.Runner
	snap       *status.Snapshot
	prev       *status.Snapshot
	err        string
	width      int
	height     int
	mode       mode
	forLogs    bool
	target     int
	action     int
	confirmOK  bool
	collecting bool
	paying     bool // a journal fetch for payouts is in flight
	busy       bool
	opResult   string
	history    []point
	lastInv    string
	ascii      bool
	font       ansi.Font // what the terminal draws (the console font on TERM=linux)
	pay        *payouts.Report
	payErr     string
	priceHTTP  *http.Client // nil = price display off (ui.price = false)
	pricing    bool         // a price request is in flight
	quote      *price.Quote // last good quote, kept on errors
	pinging    bool         // a node ping is in flight
	node       *nodePing    // last node ping
	nodeMs     []int64      // the last pings that answered (at most 5), for the median
	priceErr   string
	version    string
	host       string
	quitting   bool // final frame is empty so terminals without an alternate screen end up clean
}

type snapshotMsg *status.Snapshot
type tickMsg time.Time
type opMsg struct{ text string }
type logsMsg struct{ err error }
type payoutsMsg struct {
	rep *payouts.Report
	err error
}
type payTickMsg struct{}
type priceMsg struct {
	q   *price.Quote
	err error
}
type priceTickMsg struct{}

// nodePing is the TUI's ping of the Monero node in P2Pool's config (UI-08).
type nodePing struct {
	r   node.Result
	err string // the config could not be read
	at  time.Time
}
type nodeTickMsg struct{}
type quitMsg struct{}

func New(cfg *config.Config, c *status.Collector, version string) Model {
	lang := strings.ToUpper(os.Getenv("LC_ALL") + os.Getenv("LC_CTYPE") + os.Getenv("LANG"))
	host, _ := os.Hostname()
	m := Model{cfg: cfg, collector: c, run: systemd.ExecRunner, version: version, host: host,
		ascii: !strings.Contains(lang, "UTF-8") && !strings.Contains(lang, "UTF8"), font: ansi.ConsoleFont(os.Stdout.Fd())}
	if cfg.UI.Price {
		m.priceHTTP = priceClient(cfg)
	}
	return m
}

// priceClient takes P2Pool's socks5 path when one is configured (PRICE-02), as the node
// probes do, but with the longer price.Timeout instead of their 3 s.
func priceClient(cfg *config.Config) *http.Client {
	var d node.Dialer
	if cfg.P2Pool.ParamsFile != "" {
		if b, err := os.ReadFile(cfg.P2Pool.ParamsFile); err == nil {
			if p := node.ReadParams(b); p.Socks5 != "" {
				d = node.SOCKS5Dialer(p.Socks5)
			}
		}
	}
	c := node.NewHTTPClient(d)
	c.Timeout = price.Timeout
	return c
}

// Run starts the program; the caller checks for a TTY beforehand.
func Run(m Model) error {
	_, err := tea.NewProgram(m).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.collect(), m.tick(), (&m).fetchPayouts(), m.payTick(), (&m).fetchPrice(), (&m).pingNode(), m.nodeTick())
}

// priceTick schedules the next request once the previous one has answered: one chain,
// priceEvery after a good answer, priceRetry after a failure.
func (m Model) priceTick(failed bool) tea.Cmd {
	if m.priceHTTP == nil {
		return nil
	}
	return tea.Tick(priceWait(failed), func(time.Time) tea.Msg { return priceTickMsg{} })
}

func priceWait(failed bool) time.Duration {
	if failed {
		return priceRetry
	}
	return priceEvery
}

// fetchPrice asks for the XMR rate unless it is off or a request is running.
func (m *Model) fetchPrice() tea.Cmd {
	if m.priceHTTP == nil || m.pricing {
		return nil
	}
	m.pricing = true
	c := m.priceHTTP
	return func() tea.Msg {
		q, err := price.FetchAny(context.Background(), c, price.Providers, time.Now)
		return priceMsg{q: q, err: err}
	}
}

func (m Model) nodeTick() tea.Cmd {
	if m.cfg.P2Pool.ParamsFile == "" {
		return nil
	}
	return tea.Tick(nodeEvery, func(time.Time) tea.Msg { return nodeTickMsg{} })
}

// pingNode reads the node from P2Pool's config each time (moneroid node select
// may switch it) and pings it the way P2Pool reaches it (socks5 if set).
func (m *Model) pingNode() tea.Cmd {
	path := m.cfg.P2Pool.ParamsFile
	if path == "" || m.pinging {
		return nil
	}
	m.pinging = true
	return func() tea.Msg {
		b, err := os.ReadFile(path)
		if err != nil {
			return &nodePing{err: err.Error(), at: time.Now()}
		}
		p := node.ReadParams(b)
		if p.Node.Host == "" {
			return &nodePing{err: "no host in " + path, at: time.Now()}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return &nodePing{r: node.Ping(ctx, node.NewHTTPClient(p.Dialer()), p.Node), at: time.Now()}
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(time.Duration(m.cfg.UI.RefreshMs)*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) payTick() tea.Cmd {
	return tea.Tick(payoutsEvery, func(time.Time) tea.Msg { return payTickMsg{} })
}

// refresh starts a collection unless one is already running (DATA-01).
func (m *Model) refresh() tea.Cmd {
	if m.collecting {
		return nil
	}
	m.collecting = true
	return m.collect()
}

func (m Model) collect() tea.Cmd {
	c := m.collector
	return func() tea.Msg { return snapshotMsg(c.Collect(context.Background())) }
}

func (m Model) control(verb string, units []string) tea.Cmd {
	run, live, xmrigUnit := m.run, m.collector != nil && m.collector.HeldFile != "", m.cfg.Services.XMRig
	return func() tea.Msg {
		// GUARD-04: a manual XMRig operation cancels the guard's hold.
		if live {
			_, done, err := guard.Manual(units, xmrigUnit)
			defer done()
			if errors.Is(err, guard.ErrHoldNotCleared) {
				return opMsg{text: verb + " refused: " + err.Error() + "; run through sudo"}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := systemd.Control(ctx, run, verb, units...); err != nil {
			return opMsg{text: verb + " failed: " + err.Error()}
		}
		return opMsg{text: verb + " " + strings.Join(units, " ") + ": done"}
	}
}

// fetchPayouts greps the journal unless a fetch is already running.
func (m *Model) fetchPayouts() tea.Cmd {
	if m.paying {
		return nil
	}
	m.paying = true
	run, unit := m.run, m.cfg.Services.P2Pool
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rep, err := payouts.Fetch(ctx, run, unit, "")
		return payoutsMsg{rep: rep, err: err}
	}
}

func (m Model) units() []string {
	switch targets[m.target] {
	case "p2pool":
		return []string{m.cfg.Services.P2Pool}
	case "xmrig":
		return []string{m.cfg.Services.XMRig}
	}
	return []string{m.cfg.Services.P2Pool, m.cfg.Services.XMRig}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		if m.collecting {
			return m, m.tick() // DATA-01: skipped ticks do not queue
		}
		m.collecting = true
		return m, tea.Batch(m.collect(), m.tick())
	case snapshotMsg:
		m.collecting = false
		m.prev, m.snap = m.snap, (*status.Snapshot)(msg)
		m.record()
		return m, nil
	case opMsg:
		m.busy = false
		m.opResult = msg.text
		return m, m.refresh()
	case logsMsg:
		signal.Reset(os.Interrupt)
		return m, m.refresh()
	case quitMsg:
		return m, tea.Quit
	case payTickMsg:
		return m, tea.Batch(m.fetchPayouts(), m.payTick())
	case priceTickMsg:
		return m, m.fetchPrice()
	case nodeTickMsg:
		return m, tea.Batch(m.pingNode(), m.nodeTick())
	case *nodePing:
		m.pinging, m.node = false, msg
		if l := msg.r.LatencyMs; l != nil && msg.err == "" && msg.r.Error == "" {
			m.nodeMs = append(m.nodeMs, *l)
			if len(m.nodeMs) > 5 {
				m.nodeMs = m.nodeMs[len(m.nodeMs)-5:]
			}
		}
		return m, nil
	case priceMsg:
		m.pricing, m.priceErr = false, ""
		if msg.err != nil {
			m.priceErr = msg.err.Error() // keep the last good quote on screen
		} else {
			m.quote = msg.q
		}
		return m, m.priceTick(msg.err != nil)
	case payoutsMsg:
		m.paying, m.payErr = false, ""
		if msg.err != nil {
			m.payErr = msg.err.Error() // keep the last good report on screen
		} else {
			m.pay = msg.rep
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m Model) key(k string) (tea.Model, tea.Cmd) {
	if k == "ctrl+c" || (k == "q" && m.mode == modeDashboard) {
		m.quitting = true
		// Erase the screen ourselves before Quit: inside an alternate screen this
		// is invisible, and terminals without one (Linux console, SOL, screen
		// without altscreen) are left clean like after htop. Raw output is
		// flushed by the renderer ticker, so Quit follows one tick later.
		return m, tea.Batch(tea.Raw("\x1b[H\x1b[2J"), tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return quitMsg{} }))
	}
	switch m.mode {
	case modeDashboard:
		switch k {
		case "r":
			if !m.collecting {
				m.collecting = true
				return m, m.collect()
			}
		case "s":
			if !m.busy {
				m.mode, m.forLogs, m.target = modeTarget, false, 0
			}
		case "l":
			m.mode, m.forLogs, m.target = modeTarget, true, 0
		case "?":
			m.mode = modeHelp
		case "p":
			m.mode = modePayouts
			return m, m.fetchPayouts()
		}
	case modeTarget:
		switch k {
		case "esc", "q":
			m.mode = modeDashboard
		case "up", "k":
			m.target = (m.target + len(targets) - 1) % len(targets)
		case "down", "j":
			m.target = (m.target + 1) % len(targets)
		case "enter":
			if m.forLogs {
				m.mode = modeDashboard
				return m, m.logs()
			}
			m.mode, m.action = modeAction, 0
		}
	case modeAction:
		switch k {
		case "esc", "q":
			m.mode = modeDashboard
		case "up", "k":
			m.action = (m.action + len(actions) - 1) % len(actions)
		case "down", "j":
			m.action = (m.action + 1) % len(actions)
		case "enter":
			if actions[m.action] == "start" {
				return m.launch()
			}
			m.mode, m.confirmOK = modeConfirm, false // UI-03: Cancel is selected by default
		}
	case modeConfirm:
		switch k {
		case "esc", "q", "n":
			m.mode = modeDashboard
		case "left", "right", "tab", "h":
			m.confirmOK = !m.confirmOK
		case "enter":
			if m.confirmOK {
				return m.launch()
			}
			m.mode = modeDashboard
		}
	case modeHelp:
		m.mode = modeDashboard
	case modePayouts:
		if k == "r" {
			return m, m.fetchPayouts()
		}
		m.mode = modeDashboard
	}
	return m, nil
}

func (m Model) launch() (tea.Model, tea.Cmd) {
	m.mode = modeDashboard
	if m.busy {
		return m, nil // SYS-09: one control operation at a time
	}
	m.busy = true
	m.opResult = actions[m.action] + " " + targets[m.target] + " in progress…"
	return m, m.control(actions[m.action], m.units())
}

func (m Model) logs() tea.Cmd {
	// Ctrl-C must end journalctl only. A Go handler (not SIG_IGN, which exec
	// would inherit) keeps this process alive; the child gets the default action.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	cmd := exec.Command("journalctl", systemd.JournalArgs(m.units(), 200, true)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return logsMsg{err: err} })
}

// record appends a hashrate point (DATA-09) with a gap on a new service session (DATA-08).
func (m *Model) record() {
	s := m.snap
	inv := s.Services.XMRig.InvocationID
	if m.lastInv != "" && inv != m.lastInv {
		m.history = append(m.history, point{t: s.CollectedAt})
	}
	m.lastInv = inv
	if s.Sources[status.SrcXMRigSummary].State == status.StateOK && s.Sources[status.SrcXMRigSummary].ErrorCode == "" {
		m.history = append(m.history, point{t: s.CollectedAt, v: s.XMRig.Hashrate10s})
	} else {
		m.history = append(m.history, point{t: s.CollectedAt})
	}
	cut := s.CollectedAt.Add(-historySpan)
	for len(m.history) > historyLen || (len(m.history) > 0 && m.history[0].t.Before(cut)) {
		m.history = m.history[1:]
	}
}
