package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/p2pool"
	"github.com/Plasmoid77/moneroid/internal/xmrig"
)

// stand builds a collector over fixtures: fake systemctl, httptest XMRig, temp Data API dir.
type stand struct {
	c       *Collector
	dir     string
	show    string
	summary []byte
	now     time.Time
	mono    time.Duration
}

const showActive = `Id=moneroid-p2pool.service
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
Result=success
MainPID=100
ExecMainStatus=0
NRestarts=0
InvocationID=aaa
ExecMainStartTimestampMonotonic=1661000000
RuntimeDirectory=moneroid-p2pool-api
RuntimeDirectoryPreserve=no

Id=moneroid-xmrig.service
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
Result=success
MainPID=200
ExecMainStatus=0
NRestarts=0
InvocationID=bbb
ExecMainStartTimestampMonotonic=1891000000
RuntimeDirectory=
`

func newStand(t *testing.T) *stand {
	t.Helper()
	st := &stand{dir: t.TempDir(), show: showActive, now: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)}
	st.mono = 2000 * time.Second // p2pool uptime 339 s (file 317 s + 20 s age), xmrig uptime 109 s
	var err error
	if st.summary, err = os.ReadFile("../../testdata/xmrig/6.26.0/summary.json"); err != nil {
		t.Fatal(err)
	}
	for src, dst := range map[string]string{"local-p2p": p2pool.FileP2P, "local-stratum": p2pool.FileStratum, "network-stats": p2pool.FileNetwork, "pool-stats": p2pool.FilePool} {
		b, err := os.ReadFile("../../testdata/p2pool/4.18/" + src + ".json")
		if err != nil {
			t.Fatal(err)
		}
		st.write(t, dst, b, st.now.Add(-20*time.Second))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(st.summary) }))
	t.Cleanup(srv.Close)
	cfg := &config.Config{}
	cfg.Services.P2Pool, cfg.Services.XMRig = "moneroid-p2pool.service", "moneroid-xmrig.service"
	cfg.P2Pool.DataAPIDir = st.dir
	cfg.XMRig.ExpectedID = "moneroid-xmrig"
	st.c = &Collector{
		Cfg:       cfg,
		Run:       func(context.Context, string, ...string) ([]byte, []byte, error) { return []byte(st.show), nil, nil },
		XMRig:     &xmrig.Client{HTTP: xmrig.NewHTTPClient(time.Second), BaseURL: srv.URL},
		Now:       func() time.Time { return st.now },
		Monotonic: func() time.Duration { return st.mono },
	}
	return st
}

func (st *stand) write(t *testing.T, name string, b []byte, mtime time.Time) {
	t.Helper()
	p := filepath.Join(st.dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mtime, mtime)
}

func (st *stand) linked() { st.c.Cfg.P2Pool.DataAPIDir = "/run/moneroid-p2pool-api" }

func TestCollectOK(t *testing.T) {
	st := newStand(t)
	s := st.c.Collect(context.Background())
	// The temp dir is not the unit's RuntimeDirectory: session cannot be proven.
	if s.Health.Level != LevelUnknown || s.Sources[SrcP2PoolP2P].ErrorCode != "SOURCE_SESSION_UNKNOWN" {
		t.Fatalf("level %s, p2p %+v", s.Health.Level, s.Sources[SrcP2PoolP2P])
	}
	if *s.XMRig.Hashrate10s <= 0 || s.XMRig.Hashrate15m != nil || !*s.XMRig.Connected || *s.P2Pool.P2PConnections != 10 {
		t.Fatalf("metrics %+v %+v", s.XMRig, s.P2Pool)
	}
	if got := *s.P2Pool.ZMQAgeSeconds; got != 8+20 {
		t.Fatalf("zmq age %v", got)
	}
	if *s.Services.P2Pool.UptimeSeconds != 339 || s.Sources[SrcXMRigSummary].ErrorCode != "" {
		t.Fatalf("uptime %v xmrig src %+v", *s.Services.P2Pool.UptimeSeconds, s.Sources[SrcXMRigSummary])
	}
	if s.P2Pool.SyncState != "unknown" || s.P2Pool.Sidechain != nil {
		t.Fatal("sync_state/sidechain must stay unknown")
	}
}

func TestHealthRules(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*stand)
		level string
		code  string
	}{
		{"ok when directory is the unit's RuntimeDirectory", func(st *stand) {}, LevelOK, ""},
		{"stopped", func(st *stand) { st.show = replaceAll(showActive, "ActiveState=active", "ActiveState=inactive") }, LevelStopped, ""},
		{"one inactive", func(st *stand) {
			st.show = replaceOnce(showActive, "ActiveState=active\nSubState=running\nUnitFileState=enabled\nResult=success\nMainPID=200", "ActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nResult=success\nMainPID=0")
		}, LevelDegraded, "UNIT_NOT_ACTIVE"},
		{"failed", func(st *stand) { st.show = replaceOnce(showActive, "ActiveState=active", "ActiveState=failed") }, LevelDegraded, "UNIT_FAILED"},
		{"not found", func(st *stand) { st.show = replaceOnce(showActive, "LoadState=loaded", "LoadState=not-found") }, LevelDegraded, "UNIT_NOT_FOUND"},
		{"activating", func(st *stand) { st.show = replaceOnce(showActive, "ActiveState=active", "ActiveState=activating") }, LevelStarting, ""},
		{"masked both inactive", func(st *stand) {
			st.show = replaceAll(replaceOnce(showActive, "LoadState=loaded", "LoadState=masked"), "ActiveState=active", "ActiveState=inactive")
		}, LevelStopped, "UNIT_MASKED"},
		{"systemd unavailable", func(st *stand) {
			st.c.Run = func(context.Context, string, ...string) ([]byte, []byte, error) { return nil, nil, os.ErrNotExist }
		}, LevelUnknown, "SOURCE_UNAVAILABLE"},
		{"id mismatch", func(st *stand) { st.c.Cfg.XMRig.ExpectedID = "other" }, LevelDegraded, "SOURCE_ID_MISMATCH"},
		{"xmrig older than service", func(st *stand) {
			st.show = replaceOnce(showActive, "ExecMainStartTimestampMonotonic=1891000000", "ExecMainStartTimestampMonotonic=1950000000")
		}, LevelDegraded, "SOURCE_NOT_CURRENT_SESSION"},
		{"xmrig api down", func(st *stand) { st.c.XMRig.BaseURL = "http://127.0.0.1:1" }, LevelUnknown, "SOURCE_UNAVAILABLE"},
		{"xmrig disconnected", func(st *stand) {
			st.summary = []byte(replaceOnce(string(st.summary), `"uptime_ms": 109920`, `"uptime_ms": 0`))
		}, LevelDegraded, "XMRIG_DISCONNECTED"},
		{"hashrate zero", func(st *stand) {
			st.summary = []byte(replaceOnce(string(st.summary), `901.25`, `0`))
		}, LevelDegraded, "HASHRATE_ZERO"},
		{"hashrate missing", func(st *stand) {
			st.summary = []byte(replaceOnce(string(st.summary), `901.25`, `null`))
		}, LevelUnknown, ""},
		{"p2p stale", func(st *stand) {
			b, _ := os.ReadFile(filepath.Join(st.dir, p2pool.FileP2P))
			st.write(t, p2pool.FileP2P, b, st.now.Add(-200*time.Second))
		}, LevelDegraded, "SOURCE_STALE"},
		{"p2p future mtime", func(st *stand) {
			b, _ := os.ReadFile(filepath.Join(st.dir, p2pool.FileP2P))
			st.write(t, p2pool.FileP2P, b, st.now.Add(60*time.Second))
		}, LevelUnknown, "CLOCK_UNCERTAIN"},
		{"p2p from previous run", func(st *stand) {
			st.show = replaceOnce(showActive, "ExecMainStartTimestampMonotonic=1661000000", "ExecMainStartTimestampMonotonic=1900000000")
		}, LevelDegraded, "SOURCE_NOT_CURRENT_SESSION"},
		{"p2p missing", func(st *stand) { os.Remove(filepath.Join(st.dir, p2pool.FileP2P)) }, LevelUnknown, "SOURCE_UNAVAILABLE"},
		{"p2p corrupt", func(st *stand) { st.write(t, p2pool.FileP2P, []byte("{"), st.now) }, LevelUnknown, "SOURCE_INVALID"},
		{"no peers", func(st *stand) {
			st.write(t, p2pool.FileP2P, []byte(`{"connections":0,"uptime":50,"zmq_last_active":1}`), st.now)
		}, LevelDegraded, "P2P_NO_CONNECTIONS"},
		{"zmq old", func(st *stand) {
			st.write(t, p2pool.FileP2P, []byte(`{"connections":3,"uptime":300,"zmq_last_active":700}`), st.now)
		}, LevelDegraded, "ZMQ_ACTIVITY_OLD"},
		{"bad field type", func(st *stand) {
			st.write(t, p2pool.FileP2P, []byte(`{"connections":"3","uptime":300,"zmq_last_active":1}`), st.now)
		}, LevelUnknown, "FIELD_INVALID"},
		{"event file broken does not change health", func(st *stand) { st.write(t, p2pool.FileStratum, []byte("nope"), st.now) }, LevelOK, ""},
		{"sidechain behind peers", func(st *stand) {
			st.write(t, p2pool.FileP2P, []byte(`{"connections":3,"uptime":300,"zmq_last_active":1,"peers":["O,1,1,P2Pool v4.18,14795201,1.2.3.4:37888"]}`), st.now)
			st.write(t, p2pool.FilePool, []byte(`{"pool_statistics":{"hashRate":10000,"sidechainHeight":12,"sidechainDifficulty":100000}}`), st.now)
		}, LevelDegraded, "SIDECHAIN_BEHIND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStand(t)
			st.linked()
			// The linked directory does not exist on the test host; point the real reads at the temp dir
			// by symlinking is not possible without privileges, so keep files in st.dir and
			// only claim the link for the session check.
			st.c.Cfg.P2Pool.DataAPIDir = st.dir
			st.c.Run = func(context.Context, string, ...string) ([]byte, []byte, error) {
				return []byte(replaceAll(st.show, "RuntimeDirectory=moneroid-p2pool-api", "RuntimeDirectory="+runtimeName(st.dir))), nil, nil
			}
			tc.mut(st)
			s := st.c.Collect(context.Background())
			if s.Health.Level != tc.level {
				t.Fatalf("level %s, want %s; issues %+v; sources %+v", s.Health.Level, tc.level, s.Health.Issues, s.Sources)
			}
			if tc.code != "" && !s.HasIssue(tc.code) && !hasSourceCode(s, tc.code) {
				t.Fatalf("expected code %s; issues %+v; sources %+v", tc.code, s.Health.Issues, s.Sources)
			}
		})
	}
}

// runtimeName makes dir look like /run/<name> for the session-link check.
func runtimeName(dir string) string {
	rel, err := filepath.Rel("/run", dir)
	if err != nil {
		return ""
	}
	return rel
}

func hasSourceCode(s *Snapshot, code string) bool {
	for _, src := range s.Sources {
		if src.ErrorCode == code {
			return true
		}
	}
	return false
}

func replaceOnce(s, old, new string) string     { return replaceN(s, old, new, 1) }
func replaceAll(s, old, new string) string      { return replaceN(s, old, new, -1) }
func replaceN(s, old, new string, n int) string { return strings.Replace(s, old, new, n) }

func TestGuardHold(t *testing.T) {
	st := newStand(t)
	st.linked()
	st.c.Cfg.P2Pool.DataAPIDir = st.dir
	show := replaceOnce(showActive, "ActiveState=active\nSubState=running\nUnitFileState=enabled\nResult=success\nMainPID=200", "ActiveState=inactive\nSubState=dead\nUnitFileState=enabled\nResult=success\nMainPID=0")
	st.c.Run = func(context.Context, string, ...string) ([]byte, []byte, error) {
		return []byte(replaceAll(show, "RuntimeDirectory=moneroid-p2pool-api", "RuntimeDirectory="+runtimeName(st.dir))), nil, nil
	}
	st.write(t, p2pool.FileP2P, []byte(`{"connections":3,"uptime":300,"zmq_last_active":1,"peers":["O,1,1,P2Pool v4.18,14795201,1.2.3.4:37888"]}`), st.now)
	st.write(t, p2pool.FilePool, []byte(`{"pool_statistics":{"hashRate":10000,"sidechainHeight":12,"sidechainDifficulty":100000}}`), st.now)
	held := filepath.Join(t.TempDir(), "held")
	if err := os.WriteFile(held, []byte("reason=SIDECHAIN_BEHIND since=2026-11-05T20:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.c.HeldFile = held
	s := st.c.Collect(context.Background())
	if s.Health.Level != LevelDegraded || !s.HasIssue("GUARD_HOLD") || s.HasIssue("UNIT_NOT_ACTIVE") || !s.HasIssue("SIDECHAIN_BEHIND") {
		t.Fatalf("level %s issues %+v", s.Health.Level, s.Health.Issues)
	}
	if s.Guard == nil || s.Guard.Reason != "SIDECHAIN_BEHIND" || s.Guard.Since == nil {
		t.Fatalf("guard %+v", s.Guard)
	}
	if len(s.P2Pool.PeerHeights) != 1 || *s.P2Pool.PeerMaxHeight != 14795201 {
		t.Fatalf("peer heights %v", s.P2Pool.PeerHeights)
	}
	os.Remove(held)
	if s := st.c.Collect(context.Background()); s.Guard != nil || !s.HasIssue("UNIT_NOT_ACTIVE") {
		t.Fatalf("without marker: guard %+v issues %+v", s.Guard, s.Health.Issues)
	}
}
