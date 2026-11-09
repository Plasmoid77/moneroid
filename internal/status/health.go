package status

import "fmt"

// Evaluate fills Health per H-02..H-04 using the collected snapshot.
func Evaluate(s *Snapshot) {
	p, x := &s.Services.P2Pool, &s.Services.XMRig
	if s.Sources[SrcSystemdP2Pool].State != StateOK {
		s.Health.Level = LevelUnknown
		s.addIssue(s.Sources[SrcSystemdP2Pool].ErrorCode, "error", "systemd", SrcSystemdP2Pool, "service states are unavailable: "+s.Sources[SrcSystemdP2Pool].Message)
		return
	}
	broken := false
	for _, sv := range []*Service{p, x} {
		comp := component(s, sv)
		switch sv.LoadState {
		case "not-found":
			broken = true
			s.addIssue("UNIT_NOT_FOUND", "error", comp, "", sv.Unit+" is not installed")
		case "error", "bad-setting":
			broken = true
			s.addIssue("UNIT_FAILED", "error", comp, "", sv.Unit+" failed to load ("+sv.LoadState+")")
		case "masked":
			s.addIssue("UNIT_MASKED", "warning", comp, "", sv.Unit+" is masked; start is impossible until unmasked")
		}
		if sv.ActiveState == "failed" {
			broken = true
			s.addIssue("UNIT_FAILED", "error", comp, "", fmt.Sprintf("%s is failed (result %s)", sv.Unit, sv.Result))
		}
		if sv.LoadState == "loaded" && sv.EnabledState != "enabled" && sv.EnabledState != "" {
			s.addIssue("UNIT_DISABLED", "info", comp, "", sv.Unit+" is not enabled for automatic start")
		}
	}
	switch {
	case broken:
		s.Health.Level = LevelDegraded
	case inactive(p) && inactive(x):
		s.Health.Level = LevelStopped
	case transitional(p) || transitional(x):
		s.Health.Level = LevelStarting
	case p.ActiveState == "active" && inactive(x) && s.Guard != nil:
		// GUARD-05: the guard stopped XMRig on purpose; show why P2Pool is not ready.
		s.Health.Level = LevelDegraded
		s.addIssue("GUARD_HOLD", "warning", "xmrig", "", fmt.Sprintf("moneroid guard holds %s until P2Pool is ready (reason %s)", x.Unit, s.Guard.Reason))
		p2poolRuntime(s, func() {}, func() {})
	case p.ActiveState == "active" && inactive(x), x.ActiveState == "active" && inactive(p):
		s.Health.Level = LevelDegraded
		for _, sv := range []*Service{p, x} {
			if inactive(sv) {
				s.addIssue("UNIT_NOT_ACTIVE", "error", component(s, sv), "", sv.Unit+" is not running while its pair is")
			}
		}
	case p.ActiveState == "active" && x.ActiveState == "active":
		s.Health.Level = runtimeLevel(s)
	default:
		s.Health.Level = LevelUnknown
	}
}

// runtimeLevel applies H-03 when both services are active.
func runtimeLevel(s *Snapshot) string {
	level := LevelOK
	degrade := func() { level = LevelDegraded }
	unknown := func() {
		if level == LevelOK {
			level = LevelUnknown
		}
	}

	xs := s.Sources[SrcXMRigSummary]
	switch {
	case xs.State != StateOK:
		s.addIssue(xs.ErrorCode, "error", "xmrig", SrcXMRigSummary, "XMRig API: "+xs.Message)
		unknown()
	case xs.ErrorCode == "SOURCE_ID_MISMATCH", xs.ErrorCode == "SOURCE_NOT_CURRENT_SESSION":
		degrade()
	case xs.ErrorCode == "SOURCE_SESSION_UNKNOWN":
		unknown()
	default:
		m := &s.XMRig
		if m.Connected == nil {
			unknown()
		} else if !*m.Connected {
			s.addIssue("XMRIG_DISCONNECTED", "error", "xmrig", SrcXMRigSummary, "XMRig is not connected to its pool")
			degrade()
		}
		if m.Hashrate10s == nil {
			unknown()
		} else if *m.Hashrate10s == 0 {
			s.addIssue("HASHRATE_ZERO", "error", "xmrig", SrcXMRigSummary, "XMRig 10 s hashrate is zero")
			degrade()
		}
	}

	p2poolRuntime(s, degrade, unknown)
	return level
}

// p2poolRuntime adds the P2Pool part of H-03; it does not depend on XMRig.
func p2poolRuntime(s *Snapshot, degrade, unknown func()) {
	ps := s.Sources[SrcP2PoolP2P]
	switch {
	case ps.State == StateStale:
		s.addIssue("SOURCE_STALE", "error", "p2pool", SrcP2PoolP2P, "local/p2p has not been updated for more than 180 s")
		degrade()
	case ps.State != StateOK:
		s.addIssue(ps.ErrorCode, "error", "p2pool", SrcP2PoolP2P, "local/p2p: "+ps.Message)
		unknown()
	case ps.ErrorCode == "SOURCE_NOT_CURRENT_SESSION":
		degrade()
	case ps.ErrorCode != "":
		unknown()
	default:
		m := &s.P2Pool
		if m.P2PConnections == nil {
			unknown()
		} else if *m.P2PConnections == 0 {
			s.addIssue("P2P_NO_CONNECTIONS", "error", "p2pool", SrcP2PoolP2P, "P2Pool has no P2P connections")
			degrade()
		}
		if m.SidechainHeight == nil || m.PeerMaxHeight == nil {
			unknown() // H-03: ok needs the sidechain proven not to lag
		} else if *m.SidechainHeight+SidechainLag < *m.PeerMaxHeight {
			s.addIssue("SIDECHAIN_BEHIND", "error", "p2pool", SrcP2PoolPool, fmt.Sprintf("local sidechain height %d is behind peers (%d): still syncing or isolated; shares are not paid until it catches up", *m.SidechainHeight, *m.PeerMaxHeight))
			degrade()
		}
		if m.ZMQAgeSeconds == nil {
			unknown()
		} else if *m.ZMQAgeSeconds > ZMQOldAfter.Seconds() {
			s.addIssue("ZMQ_ACTIVITY_OLD", "warning", "p2pool", SrcP2PoolP2P, fmt.Sprintf("no ZMQ activity observed for %.0f s; the cause is unknown", *m.ZMQAgeSeconds))
			degrade()
		}
	}
}

func inactive(sv *Service) bool { return sv.ActiveState == "inactive" }
func transitional(sv *Service) bool {
	return sv.ActiveState == "activating" || sv.ActiveState == "deactivating" || sv.ActiveState == "reloading"
}

func component(s *Snapshot, sv *Service) string {
	if sv == &s.Services.P2Pool {
		return "p2pool"
	}
	return "xmrig"
}
