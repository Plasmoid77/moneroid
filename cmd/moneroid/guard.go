package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/guard"
	"github.com/Plasmoid77/moneroid/internal/node"
	"github.com/Plasmoid77/moneroid/internal/status"
	"github.com/Plasmoid77/moneroid/internal/systemd"
	"github.com/Plasmoid77/moneroid/internal/xmrig"
)

// cmdGuard runs the hold/repair loop of moneroid-guard.service (GUARD-01..08).
// It logs one line per decision to stdout (the journal), never one per poll.
func cmdGuard(cfgPath string, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "guard takes no arguments; tune it in the [guard] section of moneroid.toml")
		return exitUsage
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	token, err := cfg.ReadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: token_file: %v\n", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	g := cfg.Guard
	m := &guard.Machine{Set: guard.Settings{
		HoldAfter: g.HoldAfter, ReleaseAfter: g.ReleaseAfter, Repair: g.Repair,
		RepairAfter: time.Duration(g.RepairAfterS) * time.Second, Cooldown: time.Duration(g.RepairCooldownS) * time.Second,
	}}
	if m.Set.Repair {
		if why := repairUnsupported(cfg); why != "" {
			fmt.Println("repair disabled:", why)
			m.Set.Repair = false
		}
	}
	e := &executor{cfg: cfg}
	if unlock, err := guard.Lock(guard.LockFile, true); err != nil {
		fmt.Println("lock:", err) // the CLI then runs unserialized; holds still work
	} else {
		unlock()
	}
	m.LastRepair, m.Repaired = e.load()
	c := &status.Collector{
		Cfg:       cfg,
		Run:       systemd.ExecRunner,
		XMRig:     &xmrig.Client{HTTP: xmrig.NewHTTPClient(status.ReadTimeout), BaseURL: cfg.XMRig.APIURL, Token: token},
		Now:       time.Now,
		Monotonic: status.Monotonic,
		HeldFile:  status.GuardHeldFile,
	}
	fmt.Printf("guard started: poll %ds, hold after %d, release after %d, repair %v (after %ds, cooldown %ds)\n",
		g.IntervalS, g.HoldAfter, g.ReleaseAfter, m.Set.Repair, g.RepairAfterS, g.RepairCooldownS)

	tick := time.NewTicker(time.Duration(g.IntervalS) * time.Second)
	defer tick.Stop()
	last := ""
	for {
		o := guard.Observe(c.Collect(ctx))
		if v := verdict(o); v != last {
			fmt.Println(v)
			last = v
		}
		e.resume(ctx)
		now := time.Now()
		switch a := m.Step(o, now); a {
		case guard.Hold:
			e.locked(func() { e.hold(ctx, o.Reason) })
		case guard.Release:
			e.locked(func() { e.release(ctx) })
		case guard.Repair:
			e.locked(func() { e.repair(ctx, now) })
		}
		e.saveIncident(m.Repaired)
		select {
		case <-ctx.Done():
			fmt.Println("guard stopped; a hold, if any, stays until P2Pool is ready again or `moneroid start xmrig`")
			return exitOK
		case <-tick.C:
		}
	}
}

func verdict(o guard.Observation) string {
	switch o.Kind {
	case guard.Skip:
		return "p2pool not running: waiting"
	case guard.Ready:
		return "p2pool ready"
	}
	if o.Reason == "SIDECHAIN_BEHIND" {
		return fmt.Sprintf("p2pool not ready: SIDECHAIN_BEHIND (%d peers ahead)", o.PeersAhead)
	}
	return "p2pool not ready: " + o.Reason
}

// repairUnsupported refuses repair before anything is stopped when P2Pool's cache
// is not in the one directory the guard unit may write (GUARD-07).
func repairUnsupported(cfg *config.Config) string {
	if cfg.P2Pool.ParamsFile == "" {
		return "p2pool.params_file is not set in moneroid.toml"
	}
	b, err := os.ReadFile(cfg.P2Pool.ParamsFile)
	if err != nil {
		return err.Error()
	}
	dir := node.ReadParams(b).DataDir
	if dir == "" || filepath.Clean(dir) != guard.DataDir {
		return fmt.Sprintf("data-dir in %s must be %s (got %q)", cfg.P2Pool.ParamsFile, guard.DataDir, dir)
	}
	return ""
}

// executor performs the actions. Every unit action runs under guard.LockFile and
// re-checks the live state there, because the snapshot may be older than a
// concurrent `moneroid start|stop` (GUARD-04).
type executor struct {
	cfg      *config.Config
	at       time.Time // last repair attempt
	pending  bool      // a repair stopped P2Pool and its start is not confirmed yet
	repaired bool      // the persisted incident flag
}

func (e *executor) locked(f func()) {
	unlock, err := guard.Lock(guard.LockFile, true)
	if err != nil {
		fmt.Println("lock:", err)
	}
	defer unlock()
	f()
}

func (e *executor) control(ctx context.Context, verb, unit string) error {
	cctx, cancel := context.WithTimeout(ctx, controlDeadline)
	defer cancel()
	err := systemd.Control(cctx, systemd.ExecRunner, verb, unit)
	if err != nil {
		fmt.Printf("systemctl %s %s failed: %v\n", verb, unit, err)
	}
	return err
}

// state returns the live ActiveState, "" when systemd cannot be read.
func (e *executor) state(ctx context.Context, unit string) string {
	sctx, cancel := context.WithTimeout(ctx, verifyDeadline)
	defer cancel()
	props, err := systemd.Show(sctx, systemd.ExecRunner, unit)
	if err != nil {
		return ""
	}
	return props[unit]["ActiveState"]
}

func stopped(state string) bool { return state == "inactive" || state == "failed" }

func held() bool {
	_, err := os.Lstat(status.GuardHeldFile)
	return err == nil
}

// hold stops a running XMRig and takes ownership through the marker. An XMRig
// that is already stopped (by the user) is left alone and gets no marker.
func (e *executor) hold(ctx context.Context, reason string) error {
	x := e.cfg.Services.XMRig
	if st := e.state(ctx, x); stopped(st) {
		return nil
	} else if st == "" {
		return errors.New("XMRig state unknown")
	}
	if err := guard.WriteFile(status.GuardHeldFile, fmt.Sprintf("reason=%s since=%s\n", reason, time.Now().UTC().Format(time.RFC3339))); err != nil {
		fmt.Println("hold marker:", err)
		return err // without the marker a stop would look like the user's own and never be undone
	}
	if err := e.control(ctx, "stop", x); err != nil {
		return err
	}
	if !stopped(e.state(ctx, x)) {
		return errors.New("XMRig did not stop")
	}
	fmt.Printf("hold: stopped %s (%s)\n", x, reason)
	return nil
}

func (e *executor) release(ctx context.Context) {
	if !held() {
		return // the user took over meanwhile
	}
	x := e.cfg.Services.XMRig
	if e.control(ctx, "start", x) != nil {
		return // the marker stays; the next ready polls try again
	}
	if err := os.Remove(status.GuardHeldFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Println("hold marker:", err)
	}
	fmt.Printf("release: started %s\n", x)
}

// repair is GUARD-06: record the attempt, hold XMRig (abort if that fails), stop
// P2Pool and confirm it is down, move the cache aside, start P2Pool. The record
// stays "pending" until P2Pool is confirmed running again (resume retries it).
func (e *executor) repair(ctx context.Context, now time.Time) {
	fmt.Println("repair: P2Pool stays behind its peers and does not catch up; resetting its sidechain cache")
	e.at, e.repaired = now, true
	if err := e.save("pending"); err != nil {
		fmt.Println("repair aborted: state file:", err)
		return
	}
	if err := e.hold(ctx, "REPAIR"); err != nil {
		fmt.Println("repair aborted: XMRig could not be held:", err)
		e.save("done")
		return
	}
	p := e.cfg.Services.P2Pool
	if e.control(ctx, "stop", p) != nil || !stopped(e.state(ctx, p)) {
		fmt.Println("repair aborted: P2Pool did not stop; nothing was moved")
		e.save("done")
		return
	}
	e.pending = true
	if err := guard.MoveCacheAside(guard.DataDir); err != nil {
		fmt.Println("repair: moving the cache aside:", err)
	} else {
		fmt.Printf("repair: moved %s to *.guard-old\n", strings.Join(guard.CacheFiles, ", "))
	}
	e.startP2Pool(ctx)
}

func (e *executor) startP2Pool(ctx context.Context) {
	p := e.cfg.Services.P2Pool
	if e.control(ctx, "start", p) == nil && e.state(ctx, p) == "active" {
		fmt.Printf("repair: started %s; it now resyncs the sidechain\n", p)
		e.pending = false
		if err := e.save("done"); err != nil {
			fmt.Println("state file:", err)
		}
	}
}

// resume finishes a repair whose P2Pool start failed or was interrupted by a crash.
func (e *executor) resume(ctx context.Context) {
	if !e.pending {
		return
	}
	e.locked(func() {
		if e.state(ctx, e.cfg.Services.P2Pool) == "active" {
			e.pending = false
			e.save("done")
			return
		}
		fmt.Println("repair: P2Pool is still down after a repair; starting it again")
		e.startP2Pool(ctx)
	})
}

func (e *executor) save(phase string) error {
	inc := "closed"
	if e.repaired {
		inc = "open"
	}
	return guard.WriteFile(guard.StateFile, fmt.Sprintf("%s %s %s\n", e.at.UTC().Format(time.RFC3339), phase, inc))
}

func (e *executor) saveIncident(repaired bool) {
	if repaired == e.repaired || e.at.IsZero() {
		return
	}
	e.repaired = repaired
	phase := "done"
	if e.pending {
		phase = "pending"
	}
	if err := e.save(phase); err != nil {
		fmt.Println("state file:", err)
	}
}

// load reads StateFile: last repair time, the incident flag and an unfinished repair.
func (e *executor) load() (time.Time, bool) {
	b, err := os.ReadFile(guard.StateFile)
	if err != nil {
		return time.Time{}, false
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, f[0])
	if err != nil {
		return time.Time{}, false
	}
	e.at, e.pending, e.repaired = at, f[1] == "pending", len(f) > 2 && f[2] == "open"
	if e.pending {
		fmt.Println("recovery: the last repair did not confirm P2Pool running; checking it")
	}
	return at, e.repaired
}
