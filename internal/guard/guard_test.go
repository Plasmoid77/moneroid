package guard

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Plasmoid77/moneroid/internal/status"
)

var t0 = time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)

func settings() Settings {
	return Settings{HoldAfter: 3, ReleaseAfter: 3, Repair: true, RepairAfter: 20 * time.Minute, Cooldown: time.Hour}
}

func i64(v int64) *int64        { return &v }
func f64(v float64) *float64    { return &v }
func tm(v time.Time) *time.Time { return &v }

// snap is a healthy shipped-profile snapshot at sidechain height h with peers at peer heights.
func snap(at time.Time, h int64, peers ...int64) *status.Snapshot {
	s := &status.Snapshot{Sources: map[string]status.Source{
		status.SrcSystemdP2Pool: {State: status.StateOK},
		status.SrcP2PoolP2P:     {State: status.StateOK, SourceUpdatedAt: tm(at)},
		status.SrcP2PoolPool:    {State: status.StateOK, AgeSeconds: f64(10)},
	}}
	s.Services.P2Pool = status.Service{ActiveState: "active", UptimeSeconds: f64(3600)}
	s.Services.XMRig = status.Service{ActiveState: "active"}
	m := &s.P2Pool
	m.P2PConnections, m.ZMQAgeSeconds, m.SidechainHeight = i64(int64(len(peers))), f64(5), i64(h)
	m.PeerHeights = peers
	if len(peers) > 0 {
		max := peers[0]
		for _, p := range peers {
			if p > max {
				max = p
			}
		}
		m.PeerMaxHeight = &max
	}
	return s
}

func TestObserve(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*status.Snapshot)
		kind   Kind
		reason string
	}{
		{"ready", func(*status.Snapshot) {}, Ready, ""},
		{"p2pool stopped", func(s *status.Snapshot) { s.Services.P2Pool.ActiveState = "inactive" }, Skip, ""},
		{"systemd unreadable", func(s *status.Snapshot) {
			s.Sources[status.SrcSystemdP2Pool] = status.Source{State: status.StateUnavailable}
		}, Skip, ""},
		{"stale", func(s *status.Snapshot) { s.Sources[status.SrcP2PoolP2P] = status.Source{State: status.StateStale} }, NotReady, "SOURCE_STALE"},
		{"previous run", func(s *status.Snapshot) {
			s.Sources[status.SrcP2PoolP2P] = status.Source{State: status.StateOK, ErrorCode: "SOURCE_NOT_CURRENT_SESSION", SourceUpdatedAt: tm(t0)}
		}, NotReady, "P2P_UNTRUSTED"},
		{"no peers", func(s *status.Snapshot) { s.P2Pool.P2PConnections = i64(0) }, NotReady, "P2P_NO_CONNECTIONS"},
		{"zmq old", func(s *status.Snapshot) { s.P2Pool.ZMQAgeSeconds = f64(700) }, NotReady, "ZMQ_ACTIVITY_OLD"},
		{"pool stats old", func(s *status.Snapshot) {
			s.Sources[status.SrcP2PoolPool] = status.Source{State: status.StateOK, AgeSeconds: f64(400)}
		}, NotReady, "POOL_STATS_UNKNOWN"},
		{"peers unknown", func(s *status.Snapshot) { s.P2Pool.PeerMaxHeight, s.P2Pool.PeerHeights = nil, nil }, NotReady, "PEERS_UNKNOWN"},
		{"behind", func(s *status.Snapshot) {
			s.P2Pool.SidechainHeight = i64(12)
		}, NotReady, "SIDECHAIN_BEHIND"},
	}
	for _, tc := range cases {
		s := snap(t0, 15220000, 15220001, 15220002, 15220000)
		tc.mut(s)
		o := Observe(s)
		if o.Kind != tc.kind || o.Reason != tc.reason {
			t.Errorf("%s: got %v %q, want %v %q", tc.name, o.Kind, o.Reason, tc.kind, tc.reason)
		}
	}
	o := Observe(snap(t0, 12, 15220001, 15220002, 100))
	if o.PeersAhead != 2 || o.Gap != 15220002-12 {
		t.Errorf("peers ahead %d gap %d", o.PeersAhead, o.Gap)
	}
}

// run feeds one observation per 30 s and returns the actions taken.
type sim struct {
	t   *testing.T
	m   *Machine
	now time.Time
	o   Observation
}

func newSim(t *testing.T) *sim {
	return &sim{t: t, m: &Machine{Set: settings()}, now: t0, o: Observation{Kind: Ready, XMRigActive: true, Uptime: 3600}}
}

func (s *sim) step() Action {
	s.now = s.now.Add(30 * time.Second)
	s.o.At = s.now // a new local/p2p write each poll
	s.o.Uptime += 30
	a := s.m.Step(s.o, s.now)
	switch a { // what the executor does
	case Hold:
		s.o.XMRigActive, s.o.Held = false, true
	case Release:
		s.o.XMRigActive, s.o.Held = true, false
	case Repair:
		s.o.XMRigActive, s.o.Held = false, true
	}
	return a
}

func (s *sim) expect(n int, want ...Action) {
	s.t.Helper()
	var got []Action
	for i := 0; i < n; i++ {
		if a := s.step(); a != None {
			got = append(got, a)
		}
	}
	if len(got) != len(want) {
		s.t.Fatalf("actions %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			s.t.Fatalf("actions %v, want %v", got, want)
		}
	}
}

func TestBootSyncHoldsThenReleases(t *testing.T) {
	s := newSim(t)
	s.o = Observation{Kind: NotReady, Reason: "SIDECHAIN_BEHIND", XMRigActive: true, Uptime: 0, PeersAhead: 5, Gap: 3000}
	s.expect(2)       // below hold_after
	s.expect(1, Hold) // third poll
	s.expect(4)       // syncing (2.5 min total): no repair, uptime too low
	s.o.Kind, s.o.Reason, s.o.Gap = Ready, "", 0
	s.expect(2)
	s.expect(1, Release)
	s.expect(10)
}

func TestReadyNeedsFreshFile(t *testing.T) {
	m := &Machine{Set: settings()}
	o := Observation{Kind: Ready, Held: true, At: t0}
	for i := 0; i < 5; i++ {
		if a := m.Step(o, t0.Add(time.Duration(i)*30*time.Second)); a != None && i < 2 {
			t.Fatalf("poll %d: %v", i, a)
		} else if a == Release {
			t.Fatalf("released on one repeated observation")
		}
	}
}

func TestIsolatedChainRepairsOncePerIncident(t *testing.T) {
	s := newSim(t)
	s.o = Observation{Kind: NotReady, Reason: "SIDECHAIN_BEHIND", XMRigActive: true, Uptime: 3600, PeersAhead: 5, Gap: 15000000}
	s.expect(3, Hold)
	s.expect(36)        // 18 min more: 19.5 min since the incident began
	s.expect(2, Repair) // 20 min reached
	s.expect(200)       // still isolated after the repair: no second attempt in this incident
	s.o.Kind, s.o.Reason = Ready, ""
	s.expect(3, Release) // the incident ends
	s.o.Kind, s.o.Reason = NotReady, "SIDECHAIN_BEHIND"
	s.expect(3, Hold)
	s.expect(40, Repair) // a new incident, cooldown (1 h) long passed
}

func TestRepairRespectsCooldownAcrossRestarts(t *testing.T) {
	s := newSim(t)
	s.m.LastRepair = t0.Add(-30 * time.Minute) // from StateFile
	s.o = Observation{Kind: NotReady, Reason: "SIDECHAIN_BEHIND", XMRigActive: true, Uptime: 3600, PeersAhead: 5, Gap: 100000}
	s.expect(3, Hold)
	s.expect(56)        // 28 min later: 20 min of incident but only 58 min since the last repair
	s.expect(5, Repair) // the cooldown expires at 60 min
}

func TestNoRepairWhenCatchingUpOrUncorroborated(t *testing.T) {
	for name, mut := range map[string]func(*sim){
		"catching up":  func(s *sim) { s.o.Gap-- },
		"one peer":     func(s *sim) { s.o.PeersAhead = 1 },
		"repair off":   func(s *sim) { s.m.Set.Repair = false },
		"young p2pool": func(s *sim) { s.o.Uptime = 0 },
	} {
		s := newSim(t)
		s.o = Observation{Kind: NotReady, Reason: "SIDECHAIN_BEHIND", XMRigActive: true, Uptime: 3600, PeersAhead: 5, Gap: 100000}
		var got []Action
		for i := 0; i < 100; i++ {
			mut(s)
			if name == "young p2pool" {
				s.o.Uptime = -30 // step adds 30: always 0
			}
			if a := s.step(); a != None {
				got = append(got, a)
			}
		}
		if len(got) != 1 || got[0] != Hold {
			t.Errorf("%s: actions %v, want only Hold", name, got)
		}
	}
}

func TestNodeDownHoldsWithoutRepair(t *testing.T) {
	s := newSim(t)
	s.o = Observation{Kind: NotReady, Reason: "ZMQ_ACTIVITY_OLD", XMRigActive: true, Uptime: 3600}
	s.expect(3, Hold)
	s.expect(200)
	// Alternating reasons break the incident: no repair either.
	for i := 0; i < 100; i++ {
		s.o.Reason = []string{"SIDECHAIN_BEHIND", "P2P_NO_CONNECTIONS"}[i%2]
		s.o.PeersAhead, s.o.Gap = 5, 100000
		if a := s.step(); a != None {
			t.Fatalf("poll %d: %v", i, a)
		}
	}
}

func TestUserIntent(t *testing.T) {
	s := newSim(t)
	s.o = Observation{Kind: NotReady, Reason: "P2P_NO_CONNECTIONS", XMRigActive: true, Uptime: 3600}
	s.expect(3, Hold)
	// `moneroid stop xmrig` removes the marker: the guard must not start XMRig later.
	s.o.Held = false
	s.o.Kind, s.o.Reason = Ready, ""
	s.expect(10)
	// `moneroid start xmrig` while P2Pool is not ready: held again after hold_after polls.
	s.o.Kind, s.o.Reason, s.o.XMRigActive = NotReady, "P2P_NO_CONNECTIONS", true
	s.expect(3, Hold)
}

func TestSkipKeepsIncidentRepaired(t *testing.T) {
	s := newSim(t)
	s.o = Observation{Kind: NotReady, Reason: "SIDECHAIN_BEHIND", XMRigActive: true, Uptime: 3600, PeersAhead: 5, Gap: 1000}
	s.expect(3, Hold)
	s.expect(38, Repair)
	s.o.Kind = Skip // P2Pool restarting after our repair
	s.expect(2)
	s.m.LastRepair = time.Time{} // even without a cooldown the incident allows one repair
	s.o.Kind, s.o.Uptime = NotReady, 3600
	s.expect(100)
}

func TestRestartKeepsIncidentRepaired(t *testing.T) {
	s := newSim(t)
	s.o = Observation{Kind: NotReady, Reason: "SIDECHAIN_BEHIND", XMRigActive: true, Uptime: 3600, PeersAhead: 5, Gap: 1000}
	s.expect(3, Hold)
	s.expect(38, Repair)
	// The guard restarts in the same incident: a new Machine loaded from StateFile.
	s.m = &Machine{Set: settings(), LastRepair: s.m.LastRepair, Repaired: s.m.Repaired}
	s.expect(400) // 3 h: cooldown long over, still no second repair without a Ready in between
	s.o.Kind, s.o.Reason = Ready, ""
	s.expect(3, Release)
	if s.m.Repaired {
		t.Fatal("Ready must close the incident")
	}
}

func TestMoveCacheAside(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"p2pool.cache", "p2pool_peers.txt", "p2pool.blocks"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := MoveCacheAside(dir); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]bool{"p2pool.cache": false, "p2pool_peers.txt": false, "p2pool.cache.guard-old": true, "p2pool_peers.txt.guard-old": true, "p2pool.blocks": true} {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != want {
			t.Errorf("%s exists=%v", n, err == nil)
		}
	}
	if err := MoveCacheAside(dir); err != nil { // nothing left to move is fine
		t.Fatal(err)
	}
	// A symlinked cache name must not move a file outside dir.
	outside := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(outside, []byte("x"), 0o600)
	os.Symlink(outside, filepath.Join(dir, "p2pool.cache"))
	MoveCacheAside(dir)
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file touched: %v", err)
	}
}

func TestLockAndWriteFile(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "lock")
	if _, err := Lock(lock, false); err == nil {
		t.Fatal("missing lock file must not be created by a client")
	}
	unlock, err := Lock(lock, true)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() { u, _ := Lock(lock, false); close(got); u() }()
	select {
	case <-got:
		t.Fatal("second lock acquired while held")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	<-got

	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(fifo, false); err == nil {
		t.Fatal("FIFO accepted as lock")
	}
	victim := filepath.Join(dir, "victim")
	os.WriteFile(victim, []byte("keep"), 0o600)
	os.Symlink(victim, filepath.Join(dir, "link"))
	if _, err := Lock(filepath.Join(dir, "link"), true); err == nil {
		t.Fatal("symlink accepted as lock")
	}

	held := filepath.Join(dir, "held")
	os.Symlink(victim, held+".tmp") // the old predictable name must not matter
	if err := WriteFile(held, "reason=X\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("victim overwritten")
	}
	if b, _ := os.ReadFile(held); string(b) != "reason=X\n" {
		t.Fatalf("held = %q", b)
	}
}
