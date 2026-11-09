package tui

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
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
	m.priceHTTP, m.quote = &http.Client{}, &price.Quote{USD: f(599.02), EUR: f(517.4), At: time.Now()}
	raw := m.View().Content
	if !strings.Contains(raw, "\x1b[38;2;255;102;0m") {
		t.Fatal("dashboard must carry Monero orange")
	}
	v := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(raw, "")
	if got := len(strings.Split(v, "\n")); got != m.height {
		t.Fatalf("dashboard must fill %d rows, got %d", m.height, got)
	}
	for _, want := range []string{"moneroid test", "health OK", "14402", "1 payouts", "0.000411000000", "12 pool blocks", "0.175", "12345680", "active/running",
		"1 XMR = $599.02 · €517.40", "CoinGecko", "≈ $0.25 · €0.21", "0.000915 XMR/day ≈ $0.55 · €0.47", "XMR/month ≈ $16.43 · €14.20"} {
		if !strings.Contains(v, want) {
			t.Fatalf("dashboard lacks %q:\n%s", want, v)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + v)
	}

	// The Linux console's kernel font has only CP437: no € — … or ▁▂▃▅▆▇ there.
	m.vt = true
	for _, md := range []mode{modeDashboard, modePayouts} {
		m.mode = md
		v := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(m.View().Content, "")
		for _, r := range v {
			if r > 0x7e && !strings.ContainsRune("·≈←↑→↓§█▓▒░▄", r) {
				t.Fatalf("Linux console: %q is not in the kernel font:\n%s", r, v)
			}
		}
		if !strings.Contains(v, "EUR 517.40") && !strings.Contains(v, "EUR 0.21") {
			t.Fatalf("Linux console: no EUR amount:\n%s", v)
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
