package status

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/jsonx"
	"github.com/Plasmoid77/moneroid/internal/p2pool"
	"github.com/Plasmoid77/moneroid/internal/systemd"
	"github.com/Plasmoid77/moneroid/internal/xmrig"
)

// Product freshness policy (contracts §6).
const (
	CollectDeadline  = 3 * time.Second
	ReadTimeout      = 1 * time.Second
	P2PStaleAfter    = 180 * time.Second
	ClockSkewMax     = 5 * time.Second
	ZMQOldAfter      = 600 * time.Second
	SessionTolerance = 5.0
	// SidechainLag is how far the local sidechain may trail connected peers before SIDECHAIN_BEHIND.
	SidechainLag = 100
)

// Collector gathers one Snapshot from systemd, XMRig and P2Pool independently.
type Collector struct {
	Cfg       *config.Config
	Run       systemd.Runner
	XMRig     *xmrig.Client
	Now       func() time.Time
	Monotonic func() time.Duration
	// HeldFile is the guard hold marker (GuardHeldFile); empty skips the check.
	HeldFile string
}

// Collect never fails: every problem is recorded in Sources and Health.
func (c *Collector) Collect(ctx context.Context) *Snapshot {
	start := c.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, CollectDeadline)
	defer cancel()
	s := &Snapshot{SchemaVersion: 1, CollectedAt: start.UTC(), Sources: map[string]Source{}}
	s.Services.P2Pool.Unit = c.Cfg.Services.P2Pool
	s.Services.XMRig.Unit = c.Cfg.Services.XMRig
	s.P2Pool.SyncState = StateUnknown
	s.Health.Issues = []Issue{}

	var (
		wg    sync.WaitGroup
		props map[string]systemd.Props
		perr  error
		sum   *xmrig.Summary
		xerr  error
		files map[string]p2pool.File
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		sctx, cancel := context.WithTimeout(ctx, ReadTimeout)
		defer cancel()
		props, perr = systemd.Show(sctx, c.Run, c.Cfg.Services.P2Pool, c.Cfg.Services.XMRig)
	}()
	go func() {
		defer wg.Done()
		xctx, cancel := context.WithTimeout(ctx, ReadTimeout)
		defer cancel()
		sum, xerr = c.XMRig.Fetch(xctx)
	}()
	go func() {
		defer wg.Done()
		files = p2pool.ReadDir(c.Cfg.P2Pool.DataAPIDir)
	}()
	wg.Wait()
	now := c.Now().UTC()
	mono := c.Monotonic().Seconds()

	c.applySystemd(s, props, perr, now, mono)
	c.applyXMRig(s, sum, xerr, now)
	c.applyP2Pool(s, files, now)
	if c.HeldFile != "" {
		s.Guard = ReadHeld(c.HeldFile)
	}
	Evaluate(s)
	s.CollectionDurationMs = c.Now().Sub(start).Milliseconds()
	return s
}

func (c *Collector) applySystemd(s *Snapshot, props map[string]systemd.Props, err error, now time.Time, mono float64) {
	if err != nil {
		src := Source{State: StateUnavailable, ErrorCode: "SOURCE_UNAVAILABLE", Message: sanitize(err.Error()), ObservedAt: tp(now)}
		if isPermission(err) {
			src.State, src.ErrorCode = StatePermissionDenied, "PERMISSION_DENIED"
		}
		s.Sources[SrcSystemdP2Pool] = src
		s.Sources[SrcSystemdXMRig] = src
		return
	}
	s.Props = map[string]map[string]string{}
	for u, p := range props {
		s.Props[u] = p
	}
	s.Services.P2Pool = serviceFrom(c.Cfg.Services.P2Pool, props[c.Cfg.Services.P2Pool], mono)
	s.Services.XMRig = serviceFrom(c.Cfg.Services.XMRig, props[c.Cfg.Services.XMRig], mono)
	ok := Source{State: StateOK, ObservedAt: tp(now), LastSuccessAt: tp(now)}
	s.Sources[SrcSystemdP2Pool] = ok
	s.Sources[SrcSystemdXMRig] = ok
}

func serviceFrom(unit string, p systemd.Props, mono float64) Service {
	sv := Service{
		Unit:            unit,
		LoadState:       p["LoadState"],
		ActiveState:     p["ActiveState"],
		SubState:        p["SubState"],
		EnabledState:    p["UnitFileState"],
		InvocationID:    p["InvocationID"],
		Result:          p["Result"],
		runtimeDir:      p["RuntimeDirectory"],
		runtimePreserve: p["RuntimeDirectoryPreserve"],
	}
	sv.RestartCount = parseInt(p["NRestarts"])
	sv.LastExitStatus = parseInt(p["ExecMainStatus"])
	if pid := parseInt(p["MainPID"]); pid != nil && *pid > 0 {
		sv.PID = pid
	}
	if us := parseInt(p["ExecMainStartTimestampMonotonic"]); us != nil && *us > 0 && sv.ActiveState == "active" {
		st := float64(*us) / 1e6
		if mono > st {
			up := mono - st
			sv.UptimeSeconds = &up
		}
	}
	return sv
}

func (c *Collector) applyXMRig(s *Snapshot, sum *xmrig.Summary, err error, now time.Time) {
	src := Source{ObservedAt: tp(now)}
	if err != nil {
		var ae *xmrig.AuthError
		switch {
		case errors.As(err, &ae):
			src.State, src.ErrorCode = StatePermissionDenied, "PERMISSION_DENIED"
		case isInvalidJSON(err):
			src.State, src.ErrorCode = StateInvalid, "SOURCE_INVALID"
		default:
			src.State, src.ErrorCode = StateUnavailable, "SOURCE_UNAVAILABLE"
		}
		src.Message = sanitize(err.Error())
		s.Sources[SrcXMRigSummary] = src
		return
	}
	src.State, src.LastSuccessAt = StateOK, tp(now)
	m := &s.XMRig
	m.Version, m.ID, m.UptimeSeconds = sanitize(sum.Version), sanitize(sum.ID), sum.UptimeSeconds
	m.Hashrate10s, m.Hashrate60s, m.Hashrate15m = sum.Hashrate10s, sum.Hashrate60s, sum.Hashrate15m
	m.HugepagesAllocated, m.HugepagesTotal = sum.HugepagesAllocated, sum.HugepagesTotal
	if sum.HugepagesAllocated != nil && sum.HugepagesTotal != nil && *sum.HugepagesTotal > 0 {
		pct := 100 * float64(*sum.HugepagesAllocated) / float64(*sum.HugepagesTotal)
		m.HugepagesPercent = &pct
	}
	m.Accepted, m.Rejected, m.Pool, m.ConnectionUptimeMs = sum.Accepted, sum.Rejected, sanitize(sum.Pool), sum.ConnectionUptimeMs
	if sum.ConnectionUptimeMs != nil {
		conn := *sum.ConnectionUptimeMs > 0
		m.Connected = &conn
	}
	for _, f := range sum.InvalidFields {
		s.addIssue("FIELD_INVALID", "warning", "xmrig", SrcXMRigSummary, "field "+f+" has an unexpected type or value")
	}
	if sum.ID != c.Cfg.XMRig.ExpectedID {
		src.ErrorCode = "SOURCE_ID_MISMATCH"
		s.addIssue("SOURCE_ID_MISMATCH", "error", "xmrig", SrcXMRigSummary, "API id "+strconv.Quote(sanitize(sum.ID))+" does not match expected_id")
	} else if up := s.Services.XMRig.UptimeSeconds; up != nil && sum.UptimeSeconds != nil {
		if d := *up - float64(*sum.UptimeSeconds); d > SessionTolerance || d < -SessionTolerance {
			src.ErrorCode = "SOURCE_NOT_CURRENT_SESSION"
			s.addIssue("SOURCE_NOT_CURRENT_SESSION", "error", "xmrig", SrcXMRigSummary, "API uptime disagrees with the service process start")
		}
	} else if s.Services.XMRig.ActiveState == "active" {
		src.ErrorCode = "SOURCE_SESSION_UNKNOWN"
	}
	s.Sources[SrcXMRigSummary] = src
}

func (c *Collector) applyP2Pool(s *Snapshot, files map[string]p2pool.File, now time.Time) {
	m := &s.P2Pool
	p2p := c.fileSource(s, files[p2pool.FileP2P], SrcP2PoolP2P, now)
	if o := files[p2pool.FileP2P].Object; p2p.State == StateOK || p2p.State == StateStale {
		r := reader{o, s, SrcP2PoolP2P}
		m.P2PConnections = r.Int("connections")
		m.P2PIncomingConnections = r.Int("incoming_connections")
		m.PeerListSize = r.Int("peer_list_size")
		m.PeerHeights = peerHeights(o)
		m.PeerMaxHeight = maxHeight(m.PeerHeights)
		m.UptimeSeconds = r.Int("uptime")
		m.ZMQAgeAtWriteSeconds = r.Int("zmq_last_active")
		if m.ZMQAgeAtWriteSeconds != nil && p2p.AgeSeconds != nil {
			age := float64(*m.ZMQAgeAtWriteSeconds) + *p2p.AgeSeconds
			m.ZMQAgeSeconds = &age
		}
		// DATA-08: files must belong to the running process.
		if p2p.State == StateOK && p2p.ErrorCode == "" {
			p2p.ErrorCode = c.p2poolSession(s, m.UptimeSeconds, p2p.AgeSeconds)
		}
	}
	s.Sources[SrcP2PoolP2P] = p2p

	st := c.fileSource(s, files[p2pool.FileStratum], SrcP2PoolStratum, now)
	if o := files[p2pool.FileStratum].Object; st.State == StateOK {
		r := reader{o, s, SrcP2PoolStratum}
		m.Hashrate15m = r.Float("hashrate_15m")
		m.Hashrate1h = r.Float("hashrate_1h")
		m.Hashrate24h = r.Float("hashrate_24h")
		m.StratumShares = r.Int("total_stratum_shares")
		m.SidechainSharesFound = r.Int("shares_found")
		m.SidechainSharesFailed = r.Int("shares_failed")
		m.AverageEffortPercent = r.Float("average_effort")
		m.CurrentEffortPercent = r.Float("current_effort")
		m.StratumConnections = r.Int("connections")
		if t := r.Int("last_share_found_time"); t != nil && *t > 0 {
			m.LastShareFoundAt = tp(time.Unix(*t, 0).UTC())
		}
	}
	s.Sources[SrcP2PoolStratum] = st

	nw := c.fileSource(s, files[p2pool.FileNetwork], SrcP2PoolNetwork, now)
	if o := files[p2pool.FileNetwork].Object; nw.State == StateOK {
		r := reader{o, s, SrcP2PoolNetwork}
		m.NetworkHeight = r.Int("height")
		m.NetworkDifficulty = r.Float("difficulty")
	}
	s.Sources[SrcP2PoolNetwork] = nw

	pl := c.fileSource(s, files[p2pool.FilePool], SrcP2PoolPool, now)
	if o := files[p2pool.FilePool].Object; pl.State == StateOK {
		r := reader{o, s, SrcP2PoolPool}
		m.PoolHashrate = r.Float("pool_statistics", "hashRate")
		m.SidechainHeight = r.Int("pool_statistics", "sidechainHeight")
		m.SidechainDifficulty = r.Float("pool_statistics", "sidechainDifficulty")
	}
	s.Sources[SrcP2PoolPool] = pl
}

// fileSource classifies one Data API file and computes its age (DATA-06/07, contracts §6).
func (c *Collector) fileSource(s *Snapshot, f p2pool.File, name string, now time.Time) Source {
	src := Source{ObservedAt: tp(now)}
	if f.Err != nil {
		switch {
		case errors.Is(f.Err, fs.ErrPermission):
			src.State, src.ErrorCode = StatePermissionDenied, "PERMISSION_DENIED"
		case errors.Is(f.Err, p2pool.ErrMissing), errors.Is(f.Err, p2pool.ErrEmpty):
			src.State, src.ErrorCode = StateUnavailable, "SOURCE_UNAVAILABLE"
		default:
			src.State, src.ErrorCode = StateInvalid, "SOURCE_INVALID"
		}
		src.Message = sanitize(f.Err.Error())
		if src.ErrorCode == "PERMISSION_DENIED" {
			src.Message += groupHint(c.Cfg.P2Pool.DataAPIDir)
		}
		return src
	}
	src.State, src.LastSuccessAt, src.SourceUpdatedAt = StateOK, tp(now), tp(f.ModTime.UTC())
	age := now.Sub(f.ModTime)
	if age < -ClockSkewMax {
		src.ErrorCode = "CLOCK_UNCERTAIN"
		s.addIssue("CLOCK_UNCERTAIN", "warning", "p2pool", name, f.Name+" has a modification time in the future")
		return src
	}
	if age < 0 {
		age = 0
	}
	src.AgeSeconds = fp(age.Seconds())
	if name == SrcP2PoolP2P && age > P2PStaleAfter {
		src.State, src.ErrorCode = StateStale, "SOURCE_STALE"
	}
	return src
}

// p2poolSession checks that local/p2p was written by the running service (DATA-08).
func (c *Collector) p2poolSession(s *Snapshot, fileUptime *int64, fileAge *float64) string {
	svc := s.Services.P2Pool
	if svc.ActiveState != "active" || svc.UptimeSeconds == nil {
		return "SOURCE_SESSION_UNKNOWN"
	}
	// The shipped unit owns the directory: systemd removes it on every stop.
	linked := svc.runtimeDir != "" && svc.runtimePreserve == "no" && filepath.Clean(c.Cfg.P2Pool.DataAPIDir) == filepath.Join("/run", svc.runtimeDir)
	if fileUptime == nil || fileAge == nil {
		if linked {
			return ""
		}
		return "SOURCE_SESSION_UNKNOWN"
	}
	if float64(*fileUptime)+*fileAge > *svc.UptimeSeconds+SessionTolerance {
		s.addIssue("SOURCE_NOT_CURRENT_SESSION", "error", "p2pool", SrcP2PoolP2P, "local/p2p uptime exceeds the service process uptime")
		return "SOURCE_NOT_CURRENT_SESSION"
	}
	if !linked {
		return "SOURCE_SESSION_UNKNOWN"
	}
	return ""
}

// peerHeights reads the height field of `local/p2p` peer strings
// ("O,44,94,P2Pool v4.9.1,14795198,84.196.182.49:37888"); nil when absent.
func peerHeights(o jsonx.Object) []int64 {
	v, ok := o["peers"].([]any)
	if !ok {
		return nil
	}
	var hs []int64
	for _, p := range v {
		s, ok := p.(string)
		if !ok {
			continue
		}
		f := strings.Split(s, ",")
		if len(f) < 5 {
			continue
		}
		if h, err := strconv.ParseInt(f[4], 10, 64); err == nil && h >= 0 {
			hs = append(hs, h)
		}
	}
	return hs
}

func maxHeight(hs []int64) *int64 {
	if len(hs) == 0 {
		return nil
	}
	max := hs[0]
	for _, h := range hs[1:] {
		if h > max {
			max = h
		}
	}
	return &max
}

// reader extracts fields from one Data API object, recording FIELD_INVALID
// for wrongly typed values and returning nil for absent ones (DATA-03).
type reader struct {
	o      jsonx.Object
	s      *Snapshot
	source string
}

func (r reader) note(fe *jsonx.FieldError) {
	if fe != nil && !fe.Missing {
		r.s.addIssue("FIELD_INVALID", "warning", "p2pool", r.source, "field "+fe.Path+" has an unexpected type or value")
	}
}

func (r reader) Int(path ...string) *int64 {
	v, fe := r.o.Int(path...)
	if fe != nil {
		r.note(fe)
		return nil
	}
	return &v
}

func (r reader) Float(path ...string) *float64 {
	v, fe := r.o.Float(path...)
	if fe != nil {
		r.note(fe)
		return nil
	}
	return &v
}

func parseInt(s string) *int64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

func tp(t time.Time) *time.Time { return &t }
func fp(f float64) *float64     { return &f }

// groupHint explains the usual cause of PERMISSION_DENIED on the Data API: the
// process does not carry the directory's group (usermod needs a new login).
func groupHint(dir string) string {
	st, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	gid := int(sys.Gid)
	if gid == os.Getgid() {
		return ""
	}
	if groups, err := os.Getgroups(); err == nil {
		for _, g := range groups {
			if g == gid {
				return ""
			}
		}
	}
	name := strconv.Itoa(gid)
	if g, err := user.LookupGroupId(name); err == nil {
		name = g.Name
	}
	return fmt.Sprintf(" (this process is not in group %s: add the user to it and log in again)", name)
}

func isPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "access denied") || strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func isInvalidJSON(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "invalid character") || strings.Contains(msg, "unexpected end of JSON") || strings.Contains(msg, "not an object") || strings.Contains(msg, "exceeds")
}

// sanitize strips terminal control characters and URL userinfo from upstream text (SEC-07, A18).
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if i := strings.Index(s, "://"); i >= 0 {
		if at := strings.IndexByte(s[i+3:], '@'); at >= 0 {
			end := i + 3 + at
			if !strings.ContainsAny(s[i+3:end], "/ ") {
				s = s[:i+3] + "***" + s[end:]
			}
		}
	}
	return s
}

// ReadHeld parses the guard hold marker ("reason=CODE since=RFC3339"); nil when absent.
func ReadHeld(path string) *GuardState {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 4096 {
		return nil
	}
	g := &GuardState{Held: true}
	for _, f := range strings.Fields(string(b)) {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "reason":
			g.Reason = sanitize(v)
		case "since":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				g.Since = tp(t.UTC())
			}
		}
	}
	return g
}
