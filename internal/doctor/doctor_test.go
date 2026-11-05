package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/status"
)

func snapshot(t *testing.T) (*config.Config, *status.Snapshot) {
	cfg := &config.Config{}
	cfg.Services.P2Pool, cfg.Services.XMRig = "p.service", "x.service"
	cfg.P2Pool.DataAPIDir = t.TempDir()
	rd, _ := filepath.Rel("/run", cfg.P2Pool.DataAPIDir)
	cfg.XMRig.ExpectedID = "id"
	ok := status.Source{State: status.StateOK, AgeSeconds: f(10)}
	s := &status.Snapshot{
		CollectedAt: time.Now(),
		Sources: map[string]status.Source{
			status.SrcSystemdP2Pool: ok, status.SrcSystemdXMRig: ok, status.SrcXMRigSummary: ok,
			status.SrcP2PoolP2P: ok, status.SrcP2PoolStratum: ok, status.SrcP2PoolNetwork: ok, status.SrcP2PoolPool: ok,
		},
		Props: map[string]map[string]string{
			"p.service": {"RuntimeDirectory": rd, "RuntimeDirectoryPreserve": "no"},
			"x.service": {"After": "p.service network.target"},
		},
	}
	s.Services.P2Pool = status.Service{Unit: "p.service", LoadState: "loaded", ActiveState: "active", SubState: "running", EnabledState: "enabled"}
	s.Services.XMRig = status.Service{Unit: "x.service", LoadState: "loaded", ActiveState: "active", SubState: "running", EnabledState: "enabled"}
	s.P2Pool.P2PConnections, s.P2Pool.ZMQAgeSeconds = i(5), f(30)
	s.XMRig.Connected, s.XMRig.Hashrate10s, s.XMRig.HugepagesPercent = b(true), f(1000), f(100)
	return cfg, s
}

func f(v float64) *float64 { return &v }
func i(v int64) *int64     { return &v }
func b(v bool) *bool       { return &v }

func run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return nil, nil, nil
}

func TestAllPass(t *testing.T) {
	cfg, s := snapshot(t)
	rep := Run(context.Background(), cfg, s, run)
	if rep.Summary[Fail] != 0 || rep.Summary[Warn] != 0 || rep.Summary[Pass] != 26 || rep.Summary[Skip] != 4 {
		t.Fatalf("summary %v checks %+v", rep.Summary, rep.Checks)
	}
}

func TestFailures(t *testing.T) {
	cfg, s := snapshot(t)
	s.Services.P2Pool.LoadState, s.Services.P2Pool.ActiveState = "not-found", "inactive"
	s.Services.XMRig.ActiveState = "failed"
	s.Props["x.service"]["Requires"] = "p.service"
	rep := Run(context.Background(), cfg, s, run)
	got := map[string]string{}
	for _, c := range rep.Checks {
		got[c.Component+"/"+c.Code] = c.Result
	}
	want := map[string]string{"p2pool/UNIT_LOADED": Fail, "p2pool/UNIT_ACTIVE": Skip, "p2pool/DATA_API_P2P": Skip, "xmrig/UNIT_ACTIVE": Fail, "xmrig/UNIT_DEPENDENCIES": Warn, "xmrig/XMRIG_API": Skip}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	if !rep.HasFail() {
		t.Fatal("expected failures")
	}
}

func TestGuardHoldIsNotAFault(t *testing.T) {
	cfg, s := snapshot(t)
	s.Services.XMRig.ActiveState = "inactive"
	s.Guard = &status.GuardState{Held: true, Reason: "SIDECHAIN_BEHIND"}
	rep := Run(context.Background(), cfg, s, run)
	for _, c := range rep.Checks {
		if c.Code == "UNIT_ACTIVE" && c.Component == "xmrig" {
			if !strings.Contains(c.Message, "GUARD_HOLD") || c.Result != Warn {
				t.Fatalf("check %+v", c)
			}
			return
		}
	}
	t.Fatal("no UNIT_ACTIVE check for xmrig")
}
