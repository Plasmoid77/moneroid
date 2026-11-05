// Package guard decides when `moneroid guard` holds XMRig and when it repairs an
// isolated P2Pool sidechain (spec §7.1, GUARD-01..08). The decision logic is pure;
// cmd/moneroid/guard.go executes the actions through systemctl.
package guard

import (
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/Plasmoid77/moneroid/internal/status"
)

const (
	// DataDir is the only directory moneroid-guard.service may write (ReadWritePaths).
	DataDir = "/var/lib/moneroid/p2pool"
	// StateFile keeps the last repair attempt across guard restarts and reboots:
	// "<RFC3339> pending|done open|closed" (open = this incident was already repaired).
	StateFile = "/var/lib/moneroid/guard/last-repair"
	// LockFile serializes the guard and `moneroid start|stop|restart` around the hold (GUARD-04).
	LockFile = "/run/moneroid-guard/lock"
	// MinPeersAhead is how many connected peers must report a higher sidechain before a repair (GUARD-06).
	MinPeersAhead = 3
)

// CacheFiles are the P2Pool files a repair moves aside (renamed to NAME.guard-old).
var CacheFiles = []string{"p2pool.cache", "p2pool_peers.txt"}

type Kind int

const (
	Skip     Kind = iota // P2Pool is not running or systemd is unreadable: no decision
	Ready                // P2Pool is synced with peers and fed by its node
	NotReady             // P2Pool runs but its shares would be worthless or it cannot work
)

// Observation is the part of a Snapshot the guard decides on.
type Observation struct {
	Kind        Kind
	Reason      string    // NotReady reason code
	At          time.Time // local/p2p write time; Ready counts only when it advances
	XMRigActive bool
	Held        bool    // the hold marker exists
	Uptime      float64 // P2Pool service uptime, seconds
	PeersAhead  int     // connected peers whose sidechain is more than SidechainLag ahead
	Gap         int64   // highest peer height minus the local sidechain height
}

// Observe classifies a snapshot independently of XMRig's state (GUARD-03).
func Observe(s *status.Snapshot) Observation {
	o := Observation{Held: s.Guard != nil}
	x := s.Services.XMRig.ActiveState
	o.XMRigActive = x == "active" || x == "activating" || x == "reloading"
	p := s.Services.P2Pool
	if s.Sources[status.SrcSystemdP2Pool].State != status.StateOK || p.ActiveState != "active" || p.UptimeSeconds == nil {
		o.Kind = Skip
		return o
	}
	o.Uptime = *p.UptimeSeconds
	notReady := func(reason string) Observation {
		o.Kind, o.Reason = NotReady, reason
		return o
	}
	p2p, pool, m := s.Sources[status.SrcP2PoolP2P], s.Sources[status.SrcP2PoolPool], &s.P2Pool
	switch {
	case p2p.State == status.StateStale:
		return notReady("SOURCE_STALE")
	case p2p.State != status.StateOK || p2p.ErrorCode != "" || p2p.SourceUpdatedAt == nil:
		return notReady("P2P_UNTRUSTED") // missing, invalid, previous run, unknown session or clock
	}
	o.At = *p2p.SourceUpdatedAt
	switch {
	case m.P2PConnections == nil || *m.P2PConnections == 0:
		return notReady("P2P_NO_CONNECTIONS")
	case m.ZMQAgeSeconds == nil || *m.ZMQAgeSeconds > status.ZMQOldAfter.Seconds():
		return notReady("ZMQ_ACTIVITY_OLD")
	case pool.State != status.StateOK || pool.ErrorCode != "" || pool.AgeSeconds == nil ||
		*pool.AgeSeconds > status.P2PStaleAfter.Seconds() || m.SidechainHeight == nil:
		return notReady("POOL_STATS_UNKNOWN")
	case m.PeerMaxHeight == nil:
		return notReady("PEERS_UNKNOWN")
	}
	for _, h := range m.PeerHeights {
		if h > *m.SidechainHeight+status.SidechainLag {
			o.PeersAhead++
		}
	}
	if o.Gap = *m.PeerMaxHeight - *m.SidechainHeight; o.Gap > status.SidechainLag {
		return notReady("SIDECHAIN_BEHIND")
	}
	o.Kind = Ready
	return o
}

type Action int

const (
	None    Action = iota
	Hold           // stop XMRig, write the marker
	Release        // start XMRig, remove the marker
	Repair         // hold, stop P2Pool, move the cache aside, start P2Pool
)

// Settings mirror config.Guard.
type Settings struct {
	HoldAfter, ReleaseAfter int
	Repair                  bool
	RepairAfter, Cooldown   time.Duration
}

// Machine counts consecutive observations. It never owns the hold: the marker
// file is the shared truth, so `moneroid stop|start xmrig` can cancel it (GUARD-04).
type Machine struct {
	Set        Settings
	LastRepair time.Time // from StateFile
	// Repaired: this incident already had its one repair; an incident ends at Ready.
	// Persisted in StateFile so a guard restart does not grant a second repair.
	Repaired bool

	ready, notReady int
	lastAt          time.Time
	behindSince     time.Time
	gapAtStart      int64
}

// Step returns the action for one observation taken at now.
func (m *Machine) Step(o Observation, now time.Time) Action {
	switch o.Kind {
	case Skip:
		// Keep Repaired: our own repair restarts P2Pool and must not open a new incident.
		m.ready, m.notReady, m.behindSince = 0, 0, time.Time{}
		return None
	case Ready:
		if !o.At.After(m.lastAt) {
			return None // the same file again is no new confirmation
		}
		m.lastAt = o.At
		m.notReady, m.behindSince, m.Repaired = 0, time.Time{}, false
		m.ready++
		if o.Held && m.ready >= m.Set.ReleaseAfter {
			return Release
		}
		return None
	}
	// NotReady counts every poll: holding on stale data is the safe side.
	m.ready = 0
	m.notReady++
	if o.Reason != "SIDECHAIN_BEHIND" {
		m.behindSince = time.Time{}
	} else if m.behindSince.IsZero() {
		m.behindSince, m.gapAtStart = now, o.Gap
	}
	if m.repairDue(o, now) {
		m.Repaired, m.LastRepair = true, now
		return Repair
	}
	if o.XMRigActive && m.notReady >= m.Set.HoldAfter {
		return Hold
	}
	return None
}

// repairDue applies GUARD-06: sustained, corroborated, not catching up, node alive, rate-limited.
func (m *Machine) repairDue(o Observation, now time.Time) bool {
	return m.Set.Repair && o.Reason == "SIDECHAIN_BEHIND" && !m.Repaired &&
		!m.behindSince.IsZero() && now.Sub(m.behindSince) >= m.Set.RepairAfter &&
		o.Uptime >= m.Set.RepairAfter.Seconds() &&
		o.PeersAhead >= MinPeersAhead && o.Gap >= m.gapAtStart &&
		(m.LastRepair.IsZero() || now.Sub(m.LastRepair) >= m.Set.Cooldown)
}

// MoveCacheAside renames CacheFiles inside dir to NAME.guard-old (one previous copy
// is kept). os.Root keeps every operation inside dir, symlinks included.
func MoveCacheAside(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var errs []error
	for _, name := range CacheFiles {
		if err := root.Rename(name, name+".guard-old"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
