package tui

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/Plasmoid77/moneroid/internal/ansi"
	"github.com/Plasmoid77/moneroid/internal/price"
	"github.com/Plasmoid77/moneroid/internal/status"
)

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("") // with the ClearScreen before Quit this leaves a clean screen even without an alternate screen
	}
	v := tea.NewView(m.font.Safe(m.render()))
	v.AltScreen = true
	v.WindowTitle = "moneroid"
	return v
}

func (m Model) render() string {
	if m.width > 0 && (m.width < minWidth || m.height < minHeight) {
		return m.compact()
	}
	w := m.width
	if w == 0 {
		w = minWidth
	}
	s := m.snap
	title := " moneroid " + m.version + "   " + m.host
	if s == nil {
		return m.fit(ansi.Banner(pad(title+"   collecting…", w))+"\n", w)
	}
	var b strings.Builder
	b.WriteString(ansi.Banner(pad(fmt.Sprintf("%s   %s   health %s   collected %s ago", title, s.CollectedAt.Local().Format("15:04:05 MST"), strings.ToUpper(s.Health.Level), ago(s.CollectedAt)), w)) + "\n\n")

	fmt.Fprintf(&b, "  %s\n", ansi.Orange(fmt.Sprintf("%-28s %-18s %-9s %-9s %s", "SERVICE", "STATE", "ENABLED", "UPTIME", "RESTARTS")))
	for _, sv := range []status.Service{s.Services.P2Pool, s.Services.XMRig} {
		state := sv.ActiveState + "/" + sv.SubState
		if sv.LoadState != "loaded" && sv.LoadState != "" {
			state = sv.LoadState
		}
		fmt.Fprintf(&b, "  %-28s %s %-9s %-9s %s\n", sv.Unit, ansi.Unit(fmt.Sprintf("%-18s", state)), sv.EnabledState, dur(sv.UptimeSeconds), i64(sv.RestartCount))
	}

	x := s.XMRig
	bw := w - 50 // meter width: label + value + trailing text fit into the rest
	if bw > 60 {
		bw = 60
	}
	fmt.Fprintf(&b, "\n  %s %s   connected %s   pool %s   %s\n", ansi.Orange("XMRIG"), x.Version, boolStr(x.Connected), x.Pool, age(s, status.SrcXMRigSummary))
	max := m.historyMax()
	frac := 0.0
	if x.Hashrate10s != nil && max > 0 {
		frac = *x.Hashrate10s / max
	}
	fmt.Fprintf(&b, "  10s %s H/s  [%s] max %.0f\n", ansi.White(fmt.Sprintf("%8s", f0(x.Hashrate10s))), ansi.Bar(bw, frac, m.ascii), max)
	rej := i64(x.Rejected)
	if x.Rejected != nil && *x.Rejected > 0 {
		rej = ansi.Red(rej)
	}
	fmt.Fprintf(&b, "  60s %s H/s   15m %s H/s   accepted %s   rejected %s\n", ansi.White(fmt.Sprintf("%8s", f0(x.Hashrate60s))), ansi.White(f0(x.Hashrate15m)), i64(x.Accepted), rej)
	hp := 0.0
	if x.HugepagesPercent != nil {
		hp = *x.HugepagesPercent / 100
	}
	fmt.Fprintf(&b, "  hugepages %5s%%  [%s]\n", f0(x.HugepagesPercent), ansi.Bar(bw, hp, m.ascii))
	if m.height == 0 || m.height >= minHeight+6 { // room to spare: keep the trace off the meter above
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  %s\n", m.sparkline(w-40))

	p := s.P2Pool
	fmt.Fprintf(&b, "\n  %s%s   p2p %s conn (%s in)   zmq %s s ago   %s\n", ansi.Orange("P2POOL"), m.p2poolVerText(), i64(p.P2PConnections), i64(p.P2PIncomingConnections), f0(p.ZMQAgeSeconds), age(s, status.SrcP2PoolP2P))
	fmt.Fprintf(&b, "  stratum %s H/s (15m)  shares %s  sidechain %s found / %s failed  %s\n", ansi.White(f0(p.Hashrate15m)), i64(p.StratumShares), ansi.White(i64(p.SidechainSharesFound)), i64(p.SidechainSharesFailed), age(s, status.SrcP2PoolStratum))
	sync := "peers —"
	if p.SidechainHeight != nil && p.PeerMaxHeight != nil && *p.PeerMaxHeight > 0 {
		sync = fmt.Sprintf("peers %d  [%s]", *p.PeerMaxHeight, ansi.Bar(20, float64(*p.SidechainHeight)/float64(*p.PeerMaxHeight), m.ascii))
	}
	fmt.Fprintf(&b, "  sidechain height %s / %s   network height %s   %s\n", ansi.White(i64(p.SidechainHeight)), sync, i64(p.NetworkHeight), age(s, status.SrcP2PoolNetwork))
	share := "—"
	if p.Hashrate15m != nil && p.PoolHashrate != nil && *p.PoolHashrate > 0 {
		share = fmt.Sprintf("%.3f", *p.Hashrate15m / *p.PoolHashrate * 100)
	}
	fmt.Fprintf(&b, "  pool %s   your share ≈ %s %%   %s\n", hs(p.PoolHashrate), ansi.White(share), age(s, status.SrcP2PoolPool))
	if l := m.nodeLine(); l != "" {
		fmt.Fprintf(&b, "  %s\n", l)
	}

	fmt.Fprintf(&b, "\n  %s   %s\n", ansi.Orange("EARNINGS"), m.priceLine())
	var r strings.Builder // after the rates line, which gets only the rows left over
	fmt.Fprintf(&r, "  %s   %s\n", ansi.Orange("paid    "), m.payoutsLine())
	fmt.Fprintf(&r, "  %s   %s\n", ansi.Orange("expected"), m.expectedLine(s))

	var t strings.Builder // the tail: warnings, issues, dialogs
	if m.prev != nil && m.prev.XMRig.Rejected != nil && x.Rejected != nil && *x.Rejected > *m.prev.XMRig.Rejected {
		t.WriteString("\n  " + ansi.Red("warning: rejected submissions increased since the previous sample") + "\n") // H-04
	}
	issues := 0
	for _, i := range s.Health.Issues {
		if i.Severity != "info" {
			if issues == 0 {
				t.WriteString("\n")
			}
			fmt.Fprintf(&t, "  %s %-24s %s\n", ansi.Severity(fmt.Sprintf("%-7s", i.Severity)), i.Code, i.Message)
			issues++
			if issues == 5 {
				break
			}
		}
	}
	if m.opResult != "" {
		fmt.Fprintf(&t, "\n  %s\n", ansi.White(m.opResult))
	}
	if d := m.dialog(); d != "" {
		t.WriteString("\n" + d)
	}
	if m.priceHTTP != nil {
		rates := strings.Split(m.ratesLine(w-13), "\n")
		if m.height > 0 { // the tail must stay visible: drop the rates rows that do not fit
			free := m.height - 1 - strings.Count(b.String()+r.String()+t.String(), "\n")
			if free < 0 {
				free = 0
			}
			rates = rates[:min(len(rates), free)]
		}
		if len(rates) > 0 {
			fmt.Fprintf(&b, "  %s   %s\n", ansi.Orange("rates   "), strings.Join(rates, "\n"))
		}
	}
	return m.fit(b.String()+r.String()+t.String(), w)
}

// nodeLine is the ping of the Monero node P2Pool uses (UI-08); "" when P2Pool's config is unknown.
func (m Model) nodeLine() string {
	n := m.node
	switch {
	case m.cfg.P2Pool.ParamsFile == "":
		return ""
	case n == nil:
		return "node   pinging…"
	case n.err != "":
		return "node   " + ansi.Red(n.err)
	}
	addr := net.JoinHostPort(n.r.Host, strconv.Itoa(n.r.RPC))
	checked := "checked " + ago(n.at) + " ago"
	if n.r.Error != "" || n.r.LatencyMs == nil {
		return fmt.Sprintf("node   %s   %s   %s", addr, ansi.Red("no answer: "+n.r.Error), checked)
	}
	sync := "synchronized"
	if n.r.Synchronized == nil || !*n.r.Synchronized {
		sync = ansi.Red("not synchronized")
	}
	ping := ansi.White(fmt.Sprintf("%d ms", *n.r.LatencyMs))
	if k := len(m.nodeMs); k > 1 { // one slow answer (it happens over a VPN) next to the usual
		ms := append([]int64(nil), m.nodeMs...)
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		ping += fmt.Sprintf(" (median %d ms of the last %d)", ms[k/2], k)
	}
	return fmt.Sprintf("node   %s   ping %s   %s   height %s   %s", addr, ping, sync, i64(n.r.Height), checked)
}

// fit pads or trims the body so the key bar sits on the last terminal row
// (htop-style); over-long lines are clipped by the renderer.
func (m Model) fit(body string, w int) string {
	if m.height > 0 {
		if lines := strings.Split(body, "\n"); len(lines) > m.height-1 {
			body = strings.Join(lines[:m.height-1], "\n") + "\n"
		}
		for strings.Count(body, "\n") < m.height-1 {
			body += "\n"
		}
	}
	return body + ansi.Banner(pad(" q quit  r refresh  s services  l logs  p payouts  ? help", w))
}

// payoutsLine is the one-line dashboard summary of the journal payouts.
func (m Model) payoutsLine() string {
	switch {
	case m.pay == nil && m.payErr != "":
		return "error: " + m.payErr
	case m.pay == nil:
		return "loading…"
	case len(m.pay.Payouts) == 0:
		return fmt.Sprintf("%s payouts yet   %d pool blocks without a share of yours", ansi.White("no"), m.pay.BlocksWithout)
	}
	last := m.pay.Payouts[len(m.pay.Payouts)-1]
	at := "unknown time"
	if !last.At.IsZero() {
		at = last.At.Local().Format("2006-01-02 15:04")
	}
	stale := ""
	if m.payErr != "" {
		stale = "   " + ansi.Red("stale: "+m.payErr)
	}
	return fmt.Sprintf("%s payouts   total %s XMR%s   last %s +%s XMR%s   %d pool blocks without a share%s",
		ansi.White(fmt.Sprint(len(m.pay.Payouts))), ansi.White(m.pay.TotalXMR), m.approx(float64(m.pay.TotalAtomicUnits)/1e12, true),
		at, last.XMR, m.approx(float64(last.AtomicUnits)/1e12, false), m.pay.BlocksWithout, stale)
}

// priceLine is the XMR rate with its source and age (PRICE-01).
func (m Model) priceLine() string {
	q := m.quote
	switch {
	case m.priceHTTP == nil:
		return "1 XMR = ?   price display off (ui.price = false)"
	case q == nil && m.priceErr != "":
		return "1 XMR = ?   price unavailable: " + m.priceErr
	case q == nil:
		return "1 XMR = …   asking the rate sources"
	}
	stale := ""
	if m.priceErr != "" {
		stale = "   " + ansi.Red("stale: no source answers")
	}
	return fmt.Sprintf("1 XMR = %s   median, %s ago%s", m.fiat(1, true), ago(q.At), stale)
}

// p2poolVerText is " v4.18.1" after the P2POOL header, like XMRig's version; "" when unknown.
func (m Model) p2poolVerText() string {
	if m.p2poolVer == "" {
		return ""
	}
	return " " + m.p2poolVer
}

// ratesLine shows each provider side by side (PRICE-01): its rate, its rate marked
// stale when the last request failed, or the reason when it never answered. Providers
// that do not fit in width go on the next lines, under the first.
func (m Model) ratesLine(width int) string {
	var parts []string
	for i, p := range price.Providers {
		var r rate
		if i < len(m.rates) {
			r = m.rates[i]
		}
		why := strings.TrimPrefix(r.err, p.Name+": ")
		switch {
		case r.q != nil && r.err != "":
			parts = append(parts, fmt.Sprintf("%s %s %s", p.Name, fiatOf(1, r.q, false), ansi.Red("stale: "+why)))
		case r.q != nil:
			parts = append(parts, fmt.Sprintf("%s %s", p.Name, fiatOf(1, r.q, false)))
		case r.err != "":
			parts = append(parts, fmt.Sprintf("%s %s", p.Name, ansi.Red(why)))
		default:
			parts = append(parts, p.Name+" …")
		}
	}
	var b strings.Builder
	n := 0
	for _, p := range parts {
		w := visible(p)
		switch {
		case n == 0:
		case n+3+w > width:
			b.WriteString("\n" + strings.Repeat(" ", 13))
			n = 0
		default:
			b.WriteString("   ")
			n += 3
		}
		b.WriteString(p)
		n += w
	}
	return b.String()
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visible is the width of s on screen: runes without colour codes.
func visible(s string) int { return utf8.RuneCountInString(sgr.ReplaceAllString(s, "")) }

// expectedLine is the long-run expectation for the current hashrate: P2Pool pays the
// same on average as solo mining, only smoother (PRICE-03).
func (m Model) expectedLine(s *status.Snapshot) string {
	h, label := s.XMRig.Hashrate15m, "15m"
	if h == nil || *h <= 0 {
		h, label = s.XMRig.Hashrate60s, "60s"
	}
	p := s.P2Pool
	if h == nil || *h <= 0 || p.NetworkDifficulty == nil || p.NetworkReward == nil {
		return "—   needs the XMRig hashrate and the P2Pool network stats"
	}
	day := price.PerDay(*h, *p.NetworkDifficulty, *p.NetworkReward)
	return fmt.Sprintf("%s XMR/day%s   %s XMR/month%s   at %s (%s), block reward %.4f XMR",
		ansi.White(fmt.Sprintf("%.6f", day)), m.approx(day, true), fmt.Sprintf("%.5f", day*30), m.approx(day*30, true),
		hs(h), label, float64(*p.NetworkReward)/1e12)
}

// fiat formats xmr in the quoted currencies, "$1.49 · €1.29"; "" without a quote.
func (m Model) fiat(xmr float64, white bool) string { return fiatOf(xmr, m.quote, white) }

// fiatOf formats xmr at quote q; "" without one.
func fiatOf(xmr float64, q *price.Quote, white bool) string {
	if q == nil {
		return ""
	}
	var parts []string
	for _, c := range []struct {
		sym  string
		rate *float64
	}{{"$", q.USD}, {"€", q.EUR}} {
		if c.rate != nil {
			v := fmt.Sprintf("%s%.2f", c.sym, xmr**c.rate)
			if white {
				v = ansi.White(v)
			}
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " · ")
}

// approx is " ≈ $x · €y" or "" without a quote.
func (m Model) approx(xmr float64, white bool) string {
	if f := m.fiat(xmr, white); f != "" {
		return " ≈ " + f
	}
	return ""
}

func pad(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func (m Model) historyMax() float64 {
	max := 0.0
	for _, p := range m.history {
		if p.v != nil && *p.v > max {
			max = *p.v
		}
	}
	return max
}

func (m Model) dialog() string {
	switch m.mode {
	case modeTarget:
		title := "Control which service?"
		if m.forLogs {
			title = "Show logs of which service?"
		}
		return list(title, targets, m.target)
	case modeAction:
		return list("Action for "+targets[m.target]+":", actions, m.action)
	case modeConfirm:
		units := strings.Join(m.units(), ", ")
		c, o := "[Cancel]", " Confirm "
		if m.confirmOK {
			c, o = " Cancel ", "[Confirm]"
		}
		return fmt.Sprintf("  %s %s?\n  %s   %s   (←/→ then Enter, Esc cancels)\n", actions[m.action], units, c, o)
	case modeHelp:
		return "  Keys: q/Ctrl-C quit (miners keep running)  r refresh  s start/stop/restart  l journalctl -f (Ctrl-C returns)  p payouts  Esc close\n  Colour (Monero orange/white) only highlights; every state is written as text. NO_COLOR disables it.\n"
	case modePayouts:
		return m.payoutsView()
	}
	return ""
}

// payoutsView lists the last payouts from the P2Pool journal in local time.
func (m Model) payoutsView() string {
	var b strings.Builder
	b.WriteString("  Payouts (P2Pool journal)\n")
	switch {
	case m.payErr != "":
		b.WriteString("    error: " + m.payErr + "\n")
	case m.pay == nil:
		b.WriteString("    loading…\n")
	case len(m.pay.Payouts) == 0:
		fmt.Fprintf(&b, "    none yet; %d pool blocks found without a share of yours in the PPLNS window\n", m.pay.BlocksWithout)
	default:
		rows := m.pay.Payouts
		if len(rows) > 12 {
			rows = rows[len(rows)-12:]
		}
		now := ""
		if m.quote != nil {
			now = "≈ AT TODAY'S RATE"
		}
		fmt.Fprintf(&b, "    %-20s %-16s %-9s %s\n", "TIME (local)", "XMR", "BLOCK", now)
		for _, p := range rows {
			at := "unknown"
			if !p.At.IsZero() {
				at = p.At.Local().Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(&b, "    %-20s %-16s %-9d %s\n", at, p.XMR, p.Block, m.fiat(float64(p.AtomicUnits)/1e12, false))
		}
		fmt.Fprintf(&b, "    %d payouts, total %s XMR%s; %d pool blocks without a payout\n", len(m.pay.Payouts), m.pay.TotalXMR, m.approx(float64(m.pay.TotalAtomicUnits)/1e12, false), m.pay.BlocksWithout)
	}
	b.WriteString("  (r reload, Esc close)\n")
	return b.String()
}

func list(title string, items []string, sel int) string {
	var b strings.Builder
	b.WriteString("  " + title + "\n")
	for i, it := range items {
		mark := "  "
		if i == sel {
			mark = "> "
		}
		b.WriteString("    " + mark + it + "\n")
	}
	b.WriteString("  (↑/↓ Enter, Esc cancels)\n")
	return b.String()
}

func (m Model) compact() string {
	var b strings.Builder
	b.WriteString("moneroid (terminal too small, need 80x24)\n")
	if m.snap != nil {
		for _, sv := range []status.Service{m.snap.Services.P2Pool, m.snap.Services.XMRig} {
			fmt.Fprintf(&b, "%s %s\n", sv.Unit, sv.ActiveState)
		}
		fmt.Fprintf(&b, "health %s\n", m.snap.Health.Level)
	}
	b.WriteString("q quit")
	return b.String()
}

// sparkline draws the last width points of the 10 s hashrate; gaps stay blank (A07).
func (m Model) sparkline(width int) string {
	if width < 10 {
		width = 10
	}
	if width > 120 {
		width = 120
	}
	ramp := m.font.Ramp()
	if m.ascii {
		ramp = []rune(".:-=+*#@")
	}
	pts := m.history
	if len(pts) > width {
		pts = pts[len(pts)-width:]
	}
	// Scaled to the window's own range, so a steady hashrate is not a flat bar at
	// the top; at least the top 10 %, so the 10 s noise does not fill the scale.
	hi, lo := 0.0, math.Inf(1)
	for _, p := range pts {
		if p.v != nil {
			hi, lo = math.Max(hi, *p.v), math.Min(lo, *p.v)
		}
	}
	lo = math.Max(0, math.Min(lo, 0.9*hi))
	var b, g strings.Builder
	for _, p := range pts {
		switch {
		case p.v == nil:
			g.WriteRune(' ')
		case *p.v == 0:
			g.WriteRune('_')
		default:
			i := len(ramp) - 1
			if hi > lo {
				i = int((*p.v - lo) / (hi - lo) * float64(len(ramp)-1))
			}
			g.WriteRune(ramp[i])
		}
	}
	b.WriteString("10 min [" + ansi.Orange(g.String()) + strings.Repeat(" ", width-len(pts)))
	fmt.Fprintf(&b, "] %.0f–%.0f H/s, %d samples", lo, hi, len(pts))
	return b.String()
}

func ago(t time.Time) string { return dur(fp(time.Since(t).Seconds())) }
func fp(f float64) *float64  { return &f }

func age(s *status.Snapshot, name string) string {
	src := s.Sources[name]
	if src.State != status.StateOK && src.State != status.StateStale {
		return "[" + src.State + "]"
	}
	if src.AgeSeconds == nil {
		return "[" + src.State + "]" // live HTTP sources carry no file age
	}
	return "[" + src.State + " " + dur(src.AgeSeconds) + " old]"
}

// hs formats a large hashrate with an SI prefix for the pool line only.
func hs(v *float64) string {
	switch {
	case v == nil:
		return "— H/s"
	case *v >= 1e9:
		return fmt.Sprintf("%.2f GH/s", *v/1e9)
	case *v >= 1e6:
		return fmt.Sprintf("%.2f MH/s", *v/1e6)
	case *v >= 1e4:
		return fmt.Sprintf("%.1f kH/s", *v/1e3)
	}
	return fmt.Sprintf("%.0f H/s", *v)
}

func i64(v *int64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprint(*v)
}

func f0(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f", *v)
}

func boolStr(b *bool) string {
	if b == nil {
		return "unknown"
	}
	if *b {
		return "yes"
	}
	return "no"
}

func dur(sec *float64) string {
	if sec == nil {
		return "—"
	}
	d := time.Duration(*sec * float64(time.Second)).Round(time.Second)
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
