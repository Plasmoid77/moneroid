package tui

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Plasmoid77/moneroid/internal/ansi"
	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/node"
	"github.com/Plasmoid77/moneroid/internal/payouts"
	"github.com/Plasmoid77/moneroid/internal/price"
	"github.com/Plasmoid77/moneroid/internal/status"
)

func model() Model {
	cfg := &config.Config{}
	cfg.Services.P2Pool, cfg.Services.XMRig = "p.service", "x.service"
	cfg.UI.RefreshMs = 1000
	return New(cfg, nil, "test")
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		next, _ := m.key(k)
		m = next.(Model)
	}
	return m
}

func TestMenuCancelIsDefault(t *testing.T) {
	m := press(model(), "s", "enter", "j", "enter")
	if m.mode != modeConfirm || m.confirmOK || actions[m.action] != "stop" {
		t.Fatalf("mode %v ok %v action %s", m.mode, m.confirmOK, actions[m.action])
	}
	m = press(m, "enter")
	if m.mode != modeDashboard || m.busy {
		t.Fatal("Enter on Cancel must not launch")
	}
	m = press(model(), "s", "j", "j", "enter", "j", "j", "enter", "right", "enter")
	if !m.busy || m.opResult != "restart xmrig in progress…" {
		t.Fatalf("busy %v result %q", m.busy, m.opResult)
	}
	if u := m.units(); len(u) != 1 || u[0] != "x.service" {
		t.Fatalf("units %v", u)
	}
	if m2 := press(m, "s"); m2.mode != modeDashboard {
		t.Fatal("menu must stay closed while an operation is in flight")
	}
}

func TestQuitNeverControls(t *testing.T) {
	m := model()
	for _, k := range []string{"q", "ctrl+c"} {
		next, cmd := m.key(k)
		if cmd == nil || !next.(Model).quitting {
			t.Fatalf("%s must quit", k)
		}
		if v := next.(Model).View(); v.Content != "" {
			t.Fatalf("final frame after %s must be empty, got %q", k, v.Content)
		}
	}
}

func TestHistoryGapsAndBounds(t *testing.T) {
	m := model()
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	snap := func(i int, inv string, hr *float64) *status.Snapshot {
		s := &status.Snapshot{CollectedAt: base.Add(time.Duration(i) * time.Second), Sources: map[string]status.Source{status.SrcXMRigSummary: {State: status.StateOK}}}
		s.Services.XMRig.InvocationID = inv
		s.XMRig.Hashrate10s = hr
		return s
	}
	v := 100.0
	for i := 0; i <= 1300; i++ {
		m.snap = snap(i, "a", &v)
		m.record()
	}
	if len(m.history) > historyLen || m.history[0].t.Before(base.Add(1300*time.Second-historySpan)) {
		t.Fatalf("history not bounded: %d", len(m.history))
	}
	m.snap = snap(1301, "b", &v)
	m.record()
	h := m.history
	if h[len(h)-2].v != nil || h[len(h)-1].v == nil {
		t.Fatal("new invocation must insert a gap before the fresh point")
	}
	zero := 0.0
	m.snap = snap(1302, "b", &zero)
	m.record()
	if p := m.history[len(m.history)-1]; p.v == nil || *p.v != 0 {
		t.Fatal("a real zero must be recorded as zero, not as a gap")
	}
	m.width = 100
	if line := m.sparkline(40); len([]rune(line)) == 0 || !contains(line, "_") {
		t.Fatalf("sparkline %q", line)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPayoutsView(t *testing.T) {
	m := model()
	m.mode = modePayouts
	if v := m.payoutsView(); !contains(v, "loading") {
		t.Fatalf("loading state: %q", v)
	}
	m.pay = &payouts.Report{Payouts: []payouts.Payout{{At: time.Date(2026, 9, 16, 0, 47, 51, 0, time.UTC), XMR: "0.001234567890", Block: 3763276}}, TotalXMR: "0.001234567890", BlocksWithout: 2}
	v := m.payoutsView()
	if !contains(v, "0.001234567890") || !contains(v, "3763276") || !contains(v, "2 pool blocks without") || !contains(v, time.Date(2026, 9, 16, 0, 47, 51, 0, time.UTC).Local().Format("2006-01-02 15:04:05")) {
		t.Fatalf("view %q", v)
	}
	m2 := press(m, "esc")
	if m2.mode != modeDashboard {
		t.Fatal("esc must close payouts")
	}
}

func TestRenderDashboardFillsScreen(t *testing.T) {
	m := model()
	m.width, m.height = 100, 30
	f := func(v float64) *float64 { return &v }
	n := func(v int64) *int64 { return &v }
	s := &status.Snapshot{CollectedAt: time.Now(), Sources: map[string]status.Source{status.SrcXMRigSummary: {State: status.StateOK, AgeSeconds: f(1)}}}
	s.Health.Level = "ok"
	s.Services.P2Pool.Unit, s.Services.P2Pool.ActiveState, s.Services.P2Pool.SubState = "p.service", "active", "running"
	s.Services.XMRig.Unit, s.Services.XMRig.ActiveState, s.Services.XMRig.SubState = "x.service", "active", "running"
	s.XMRig.Hashrate10s, s.XMRig.Hashrate60s, s.XMRig.Hashrate15m, s.XMRig.HugepagesPercent = f(14402), f(14380), f(14355), f(100)
	s.XMRig.Accepted, s.XMRig.Rejected = n(1234), n(1)
	s.P2Pool.SidechainHeight, s.P2Pool.PeerMaxHeight, s.P2Pool.Hashrate15m, s.P2Pool.PoolHashrate = n(12345678), n(12345680), f(14350), f(8200000)
	m.snap = s
	m.record()
	m.pay = &payouts.Report{Payouts: []payouts.Payout{{At: time.Now(), XMR: "0.000411000000", AtomicUnits: 411000000, Block: 1}}, TotalXMR: "0.000411000000", TotalAtomicUnits: 411000000, BlocksWithout: 12}
	s.P2Pool.NetworkDifficulty, s.P2Pool.NetworkReward = f(821571146720), n(605805580000)
	m.priceHTTP, m.quote = &http.Client{}, &price.Quote{USD: f(599.02), EUR: f(517.4), Source: "CoinPaprika", At: time.Now()}
	raw := m.View().Content
	if !strings.Contains(raw, "\x1b[38;2;255;102;0m") {
		t.Fatal("dashboard must carry Monero orange")
	}
	v := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(raw, "")
	if got := len(strings.Split(v, "\n")); got != m.height {
		t.Fatalf("dashboard must fill %d rows, got %d", m.height, got)
	}
	for _, want := range []string{"moneroid test", "health OK", "14402", "1 payouts", "0.000411000000", "12 pool blocks", "0.175", "12345680", "active/running",
		"1 XMR = $599.02 · €517.40   CoinPaprika,", "≈ $0.25 · €0.21", "0.000915 XMR/day ≈ $0.55 · €0.47", "XMR/month ≈ $16.43 · €14.20"} {
		if !strings.Contains(v, want) {
			t.Fatalf("dashboard lacks %q:\n%s", want, v)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + v)
	}

	// The Linux console draws only its font: the kernel's CP437 one, or Lat15 from console-setup.
	for _, c := range []struct {
		font ansi.Font
		eur  string
	}{{ansi.KernelFont, "EUR 0.21"}, {ansi.NewFont("€—–…·≈←↑→↓§░▒█─¯"), "€0.21"}} {
		m.font = c.font
		for _, md := range []mode{modeDashboard, modePayouts} {
			m.mode = md
			v := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(m.View().Content, "")
			for _, r := range v {
				if !c.font.Has(r) {
					t.Fatalf("console font lacks %q:\n%s", r, v)
				}
			}
			if !strings.Contains(v, c.eur) {
				t.Fatalf("console: no %q:\n%s", c.eur, v)
			}
		}
	}
}

func TestPayoutFetchGuardAndCollectingRows(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	if got := len(strings.Split(m.View().Content, "\n")); got != 24 {
		t.Fatalf("collecting screen must fill 24 rows, got %d", got)
	}
	next, cmd := m.Update(payTickMsg{})
	m = next.(Model)
	if cmd == nil || !m.paying {
		t.Fatal("tick must start a fetch")
	}
	if m2 := press(m, "p"); m2.mode != modePayouts || !m2.paying {
		t.Fatal("p must open payouts without a second fetch")
	}
	next, _ = m.Update(payoutsMsg{err: errTest})
	m = next.(Model)
	if m.paying || m.payErr == "" {
		t.Fatal("fetch result must clear the guard and record the error")
	}
}

type testErr struct{}

func (testErr) Error() string { return "boom" }

var errTest = testErr{}

func TestPeersAndNodePing(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	b := func(v bool) *bool { return &v }
	m := model()
	m.cfg.P2Pool.ParamsFile = "/etc/moneroid/p2pool.conf"
	s := &status.Snapshot{CollectedAt: time.Now(), Sources: map[string]status.Source{}}
	s.Health.Level = "ok"
	s.Health.Issues = []status.Issue{{Severity: "warn", Code: "SOME_ISSUE", Message: "must stay visible"}}
	for i := 0; i < 12; i++ {
		s.P2Pool.Peers = append(s.P2Pool.Peers, status.Peer{UptimeS: 125, PingMs: n(int64(300 - i*20)), Software: "P2Pool v4.18.1", Addr: fmt.Sprintf("192.0.2.%d:37888", i)})
	}
	s.P2Pool.Peers = append(s.P2Pool.Peers, status.Peer{Incoming: true, Addr: "[2001:db8::1]:37888"})
	m.snap = s
	m.node = &nodePing{r: node.Result{Candidate: node.Candidate{Host: "198.51.100.7", RPC: 18089}, LatencyMs: n(166), Synchronized: b(true), Height: n(3500000)}, at: time.Now()}
	strip := func(v string) string { return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(v, "") }

	m.width, m.height = 0, 0 // size unknown: every peer
	v := strip(m.View().Content)
	for _, want := range []string{"node   198.51.100.7:18089   ping 166 ms   synchronized   height 3500000", "PEERS", "ping min 80 · median 200 · max 300 ms   13 connected", "192.0.2.11:37888       80 ms  2m 05s    out  P2Pool v4.18.1", "[2001:db8::1]:37888        —", "SOME_ISSUE"} {
		if !strings.Contains(v, want) {
			t.Fatalf("lacks %q:\n%s", want, v)
		}
	}
	if strings.Index(v, "192.0.2.11:") > strings.Index(v, "192.0.2.0:") || strings.Index(v, "192.0.2.0:") > strings.Index(v, "[2001:db8::1]") {
		t.Fatalf("peers must be sorted by ping, unmeasured last:\n%s", v)
	}

	m.width, m.height = 120, 34 // a few rows to spare: the fastest peers, the issues untouched
	v = strip(m.View().Content)
	if !strings.Contains(v, "fastest shown") || !strings.Contains(v, "192.0.2.11:") || strings.Contains(v, "[2001:db8::1]") || !strings.Contains(v, "SOME_ISSUE") || len(strings.Split(v, "\n")) != 34 {
		t.Fatalf("height 34:\n%s", v)
	}

	for _, ms := range []int64{180, 175, 2192} {
		mm, _ := m.Update(&nodePing{r: node.Result{Candidate: node.Candidate{Host: "198.51.100.7", RPC: 18089}, LatencyMs: n(ms), Synchronized: b(true)}, at: time.Now()})
		m = mm.(Model)
	}
	if v = strip(m.View().Content); !strings.Contains(v, "ping 2192 ms (median 180 ms of the last 3)") {
		t.Fatalf("node median:\n%s", v)
	}
	m.node = &nodePing{r: node.Result{Candidate: node.Candidate{Host: "198.51.100.7", RPC: 18089}, Error: "connection refused"}, at: time.Now()}
	if v = strip(m.View().Content); !strings.Contains(v, "no answer: connection refused") {
		t.Fatalf("node error:\n%s", v)
	}
}

// The rate gets its own timeout (CoinGecko over a VPN on LTE takes 3-5 s) and a quick
// retry after a failure instead of the 10-minute wait.
func TestPriceTimeoutAndRetry(t *testing.T) {
	if c := priceClient(&config.Config{}); c.Timeout != price.Timeout || price.Timeout <= node.ProbeTimeout {
		t.Fatalf("price client timeout %v, want %v", c.Timeout, price.Timeout)
	}
	if priceWait(true) != priceRetry || priceWait(false) != priceEvery || priceRetry >= priceEvery {
		t.Fatalf("waits: failed %v, ok %v", priceWait(true), priceWait(false))
	}
	m := Model{priceHTTP: &http.Client{}, pricing: true}
	r, cmd := m.Update(priceMsg{err: errors.New("CoinGecko: no answer in time")})
	if mm := r.(Model); mm.pricing || cmd == nil || !strings.Contains(mm.priceLine(), "price unavailable: CoinGecko: no answer in time") {
		t.Fatalf("after a failure: pricing %v, next %v, line %q", mm.pricing, cmd != nil, mm.priceLine())
	}
	if _, cmd := (Model{}).Update(priceMsg{err: errors.New("x")}); cmd != nil {
		t.Fatal("price off must not schedule requests")
	}
}
