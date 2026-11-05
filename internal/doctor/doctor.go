// Package doctor runs the read-only checks of spec DOC-01..05 over a collected snapshot.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/node"
	"github.com/Plasmoid77/moneroid/internal/status"
	"github.com/Plasmoid77/moneroid/internal/systemd"
)

const (
	Pass = "pass"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
)

type Check struct {
	Code      string  `json:"code"`
	Component string  `json:"component"`
	Result    string  `json:"result"`
	Message   string  `json:"message"`
	Remedy    *string `json:"remedy"`
}

type Report struct {
	SchemaVersion int            `json:"schema_version"`
	CollectedAt   time.Time      `json:"collected_at"`
	Checks        []Check        `json:"checks"`
	Summary       map[string]int `json:"summary"`
}

// HasFail reports whether any check failed (exit code 1 per DOC-02).
func (r *Report) HasFail() bool { return r.Summary[Fail] > 0 }

type runner struct {
	cfg  *config.Config
	snap *status.Snapshot
	run  systemd.Runner
	rep  Report
}

func (r *runner) add(code, component, result, msg, remedy string) {
	c := Check{Code: code, Component: component, Result: result, Message: msg}
	if remedy != "" {
		c.Remedy = &remedy
	}
	r.rep.Checks = append(r.rep.Checks, c)
	r.rep.Summary[result]++
}

// Run evaluates all checks. Config errors are handled by the CLI before this point.
func Run(ctx context.Context, cfg *config.Config, snap *status.Snapshot, run systemd.Runner) Report {
	r := &runner{cfg: cfg, snap: snap, run: run, rep: Report{SchemaVersion: 1, CollectedAt: snap.CollectedAt, Checks: []Check{}, Summary: map[string]int{Pass: 0, Warn: 0, Fail: 0, Skip: 0}}}
	r.systemdChecks()
	r.p2poolChecks()
	r.xmrigChecks()
	r.localChecks(ctx)
	return r.rep
}

func (r *runner) systemdChecks() {
	s := r.snap
	if src := s.Sources[status.SrcSystemdP2Pool]; src.State != status.StateOK {
		r.add("SYSTEMD_AVAILABLE", "systemd", Fail, "systemctl show failed: "+src.Message, "check that systemd is the init system and systemctl is on PATH")
		return
	}
	r.add("SYSTEMD_AVAILABLE", "systemd", Pass, "systemctl show answered", "")
	pair := map[string]string{"p2pool": r.cfg.Services.XMRig, "xmrig": r.cfg.Services.P2Pool}
	for _, comp := range []string{"p2pool", "xmrig"} {
		sv := s.Services.P2Pool
		if comp == "xmrig" {
			sv = s.Services.XMRig
		}
		props := s.Props[sv.Unit]
		switch sv.LoadState {
		case "loaded":
			r.add("UNIT_LOADED", comp, Pass, sv.Unit+" is loaded", "")
		case "masked":
			r.add("UNIT_LOADED", comp, Pass, sv.Unit+" is loaded (masked)", "")
		default:
			r.add("UNIT_LOADED", comp, Fail, fmt.Sprintf("%s load state is %s", sv.Unit, sv.LoadState), "install the unit file into /etc/systemd/system and run systemctl daemon-reload")
			r.add("UNIT_MASKED", comp, Skip, "unit not loaded", "")
			r.add("UNIT_ENABLED", comp, Skip, "unit not loaded", "")
			r.add("UNIT_ACTIVE", comp, Skip, "unit not loaded", "")
			continue
		}
		if sv.LoadState == "masked" {
			r.add("UNIT_MASKED", comp, Fail, sv.Unit+" is masked", "systemctl unmask "+sv.Unit)
		} else {
			r.add("UNIT_MASKED", comp, Pass, sv.Unit+" is not masked", "")
		}
		if sv.EnabledState == "enabled" {
			r.add("UNIT_ENABLED", comp, Pass, sv.Unit+" is enabled", "")
		} else {
			r.add("UNIT_ENABLED", comp, Warn, fmt.Sprintf("%s is %s (UNIT_DISABLED)", sv.Unit, sv.EnabledState), "systemctl enable "+sv.Unit+" for automatic start after boot")
		}
		switch sv.ActiveState {
		case "active":
			r.add("UNIT_ACTIVE", comp, Pass, sv.Unit+" is active/"+sv.SubState, "")
		case "failed":
			r.add("UNIT_ACTIVE", comp, Fail, fmt.Sprintf("%s is failed: result=%s exit=%s restarts=%s", sv.Unit, sv.Result, i64(sv.LastExitStatus), i64(sv.RestartCount)), "moneroid logs "+comp+"; fix the native config or node, then moneroid start "+comp)
		default:
			if comp == "xmrig" && s.Guard != nil {
				// GUARD-05: a deliberate hold, not a fault; the P2Pool checks below say why.
				r.add("UNIT_ACTIVE", comp, Warn, fmt.Sprintf("%s is held by moneroid guard (GUARD_HOLD, reason %s)", sv.Unit, s.Guard.Reason), "wait: the guard starts it when P2Pool is ready; `moneroid start xmrig` cancels the hold")
			} else {
				r.add("UNIT_ACTIVE", comp, Warn, sv.Unit+" is "+sv.ActiveState, "moneroid start "+comp)
			}
		}
		if comp == "xmrig" {
			if systemd.Props(props).HasDependency("After", pair[comp]) {
				r.add("UNIT_ORDERING", comp, Pass, "After= contains "+pair[comp], "")
			} else {
				r.add("UNIT_ORDERING", comp, Warn, "After= does not contain "+pair[comp]+"; XMRig may start before P2Pool", "add After="+pair[comp]+" to the unit or a drop-in")
			}
		}
		var deps []string
		for _, prop := range []string{"Wants", "Requires", "BindsTo", "PartOf"} {
			if systemd.Props(props).HasDependency(prop, pair[comp]) {
				deps = append(deps, prop)
			}
		}
		if len(deps) == 0 {
			r.add("UNIT_DEPENDENCIES", comp, Pass, "no Wants/Requires/BindsTo/PartOf towards "+pair[comp], "")
		} else {
			r.add("UNIT_DEPENDENCIES", comp, Warn, "DEPENDENCIES_DIFFER: "+strings.Join(deps, ",")+" towards "+pair[comp]+"; single-service operations will affect both", "keep only After= between the units")
		}
		if comp == "p2pool" {
			rd := props["RuntimeDirectory"]
			if rd != "" && filepath.Join("/run", rd) == filepath.Clean(r.cfg.P2Pool.DataAPIDir) && props["RuntimeDirectoryPreserve"] == "no" {
				r.add("UNIT_RUNTIME_DIR", comp, Pass, "RuntimeDirectory matches data_api_dir and is not preserved", "")
			} else {
				r.add("UNIT_RUNTIME_DIR", comp, Warn, fmt.Sprintf("RuntimeDirectory=%q preserve=%q does not own data_api_dir %s; file freshness cannot be tied to the process", rd, props["RuntimeDirectoryPreserve"], r.cfg.P2Pool.DataAPIDir), "use RuntimeDirectory= with RuntimeDirectoryPreserve=no and point data-api there")
			}
		}
	}
}

func (r *runner) p2poolChecks() {
	s := r.snap
	if s.Services.P2Pool.ActiveState != "active" {
		for _, c := range []string{"DATA_API_DIR", "DATA_API_P2P", "DATA_API_EVENT_FILES", "P2P_CONNECTIONS", "SIDECHAIN_SYNC", "ZMQ_ACTIVITY"} {
			r.add(c, "p2pool", Skip, "P2Pool service is not active", "")
		}
		return
	}
	p2p := s.Sources[status.SrcP2PoolP2P]
	if _, err := os.ReadDir(r.cfg.P2Pool.DataAPIDir); err != nil {
		r.add("DATA_API_DIR", "p2pool", Fail, "cannot read "+r.cfg.P2Pool.DataAPIDir+": "+err.Error(), "check data-api in p2pool.conf; add the operator to the group owning the directory (log in again afterwards)")
	} else {
		r.add("DATA_API_DIR", "p2pool", Pass, r.cfg.P2Pool.DataAPIDir+" is readable", "")
	}
	switch {
	case p2p.State == status.StateOK && p2p.ErrorCode == "":
		r.add("DATA_API_P2P", "p2pool", Pass, fmt.Sprintf("local/p2p is fresh (%.0f s old) and belongs to the running process", *p2p.AgeSeconds), "")
	case p2p.State == status.StateOK:
		r.add("DATA_API_P2P", "p2pool", Warn, "local/p2p: "+p2p.ErrorCode, "restart p2pool if the file does not refresh; check the system clock")
	case p2p.State == status.StateStale:
		r.add("DATA_API_P2P", "p2pool", Warn, "local/p2p is stale (SOURCE_STALE)", "moneroid logs p2pool")
	default:
		r.add("DATA_API_P2P", "p2pool", Warn, "local/p2p: "+p2p.ErrorCode+" "+p2p.Message, "the file appears ~60 s after start; check local-api in p2pool.conf; moneroid logs p2pool")
	}
	var bad []string
	for _, n := range []string{status.SrcP2PoolStratum, status.SrcP2PoolNetwork, status.SrcP2PoolPool} {
		if src := s.Sources[n]; src.State != status.StateOK {
			bad = append(bad, n+"="+src.ErrorCode)
		}
	}
	if len(bad) == 0 {
		r.add("DATA_API_EVENT_FILES", "p2pool", Pass, "local/stratum, network/stats, pool/stats are readable", "")
	} else {
		r.add("DATA_API_EVENT_FILES", "p2pool", Warn, strings.Join(bad, " "), "event files appear after the first job/block; otherwise moneroid logs p2pool")
	}
	switch c := s.P2Pool.P2PConnections; {
	case c == nil:
		r.add("P2P_CONNECTIONS", "p2pool", Skip, "connections unknown", "")
	case *c == 0:
		r.add("P2P_CONNECTIONS", "p2pool", Warn, "no P2P connections", "check outbound connectivity to the sidechain P2P port")
	default:
		r.add("P2P_CONNECTIONS", "p2pool", Pass, fmt.Sprintf("%d P2P connections", *c), "")
	}
	switch h, ph := s.P2Pool.SidechainHeight, s.P2Pool.PeerMaxHeight; {
	case p2p.State != status.StateOK || p2p.ErrorCode != "" || s.Sources[status.SrcP2PoolPool].State != status.StateOK:
		r.add("SIDECHAIN_SYNC", "p2pool", Skip, "local/p2p or pool/stats not trustworthy for this session", "")
	case h == nil || ph == nil:
		r.add("SIDECHAIN_SYNC", "p2pool", Skip, "sidechain or peer heights unknown", "")
	case *h+status.SidechainLag < *ph:
		r.add("SIDECHAIN_SYNC", "p2pool", Warn, fmt.Sprintf("local sidechain height %d, peers report %d", *h, *ph), "wait: P2Pool downloads and verifies the PPLNS window after start (minutes); if it never catches up, check the node and moneroid logs p2pool")
	default:
		r.add("SIDECHAIN_SYNC", "p2pool", Pass, fmt.Sprintf("sidechain height %d matches peers", *h), "")
	}
	switch a := s.P2Pool.ZMQAgeSeconds; {
	case a == nil:
		r.add("ZMQ_ACTIVITY", "p2pool", Skip, "ZMQ age unknown", "")
	case *a > status.ZMQOldAfter.Seconds():
		r.add("ZMQ_ACTIVITY", "p2pool", Warn, fmt.Sprintf("no ZMQ activity for %.0f s", *a), "check the node's ZMQ port in p2pool.conf; moneroid logs p2pool")
	default:
		r.add("ZMQ_ACTIVITY", "p2pool", Pass, fmt.Sprintf("ZMQ activity %.0f s ago", *a), "")
	}
}

func (r *runner) xmrigChecks() {
	s := r.snap
	if s.Services.XMRig.ActiveState != "active" {
		for _, c := range []string{"XMRIG_API", "XMRIG_ID", "XMRIG_SESSION", "XMRIG_CONNECTED", "XMRIG_HASHRATE", "XMRIG_HUGEPAGES"} {
			r.add(c, "xmrig", Skip, "XMRig service is not active", "")
		}
		return
	}
	src := s.Sources[status.SrcXMRigSummary]
	if src.State != status.StateOK {
		r.add("XMRIG_API", "xmrig", Fail, src.ErrorCode+": "+src.Message, "enable http in xmrig.json on the loopback address from api_url; for 401/403 fix token_file")
		for _, c := range []string{"XMRIG_ID", "XMRIG_SESSION", "XMRIG_CONNECTED", "XMRIG_HASHRATE", "XMRIG_HUGEPAGES"} {
			r.add(c, "xmrig", Skip, "API unavailable", "")
		}
		return
	}
	r.add("XMRIG_API", "xmrig", Pass, "GET /2/summary answered (XMRig "+s.XMRig.Version+")", "")
	if src.ErrorCode == "SOURCE_ID_MISMATCH" {
		r.add("XMRIG_ID", "xmrig", Fail, fmt.Sprintf("API id %q differs from expected_id %q", s.XMRig.ID, r.cfg.XMRig.ExpectedID), "set api.id in xmrig.json equal to expected_id in moneroid.toml")
	} else {
		r.add("XMRIG_ID", "xmrig", Pass, "API id matches expected_id", "")
	}
	switch src.ErrorCode {
	case "SOURCE_NOT_CURRENT_SESSION":
		r.add("XMRIG_SESSION", "xmrig", Warn, "API uptime disagrees with the service process; another XMRig may own the port", "stop other XMRig instances using "+r.cfg.XMRig.APIURL)
	case "SOURCE_SESSION_UNKNOWN":
		r.add("XMRIG_SESSION", "xmrig", Warn, "cannot compare API uptime with the service process", "")
	default:
		r.add("XMRIG_SESSION", "xmrig", Pass, "API uptime matches the service process", "")
	}
	switch c := s.XMRig.Connected; {
	case c == nil:
		r.add("XMRIG_CONNECTED", "xmrig", Skip, "connection state unknown", "")
	case !*c:
		r.add("XMRIG_CONNECTED", "xmrig", Warn, "not connected to "+s.XMRig.Pool, "check pools[0].url in xmrig.json equals the P2Pool stratum address")
	default:
		r.add("XMRIG_CONNECTED", "xmrig", Pass, "connected to "+s.XMRig.Pool, "")
	}
	switch h := s.XMRig.Hashrate10s; {
	case h == nil:
		r.add("XMRIG_HASHRATE", "xmrig", Skip, "10 s hashrate not reported yet", "")
	case *h == 0:
		r.add("XMRIG_HASHRATE", "xmrig", Warn, "10 s hashrate is zero", "moneroid logs xmrig")
	default:
		r.add("XMRIG_HASHRATE", "xmrig", Pass, fmt.Sprintf("10 s hashrate %.0f H/s", *h), "")
	}
	switch p := s.XMRig.HugepagesPercent; {
	case p == nil:
		r.add("XMRIG_HUGEPAGES", "xmrig", Skip, "hugepages not reported", "")
	case *p < 100:
		r.add("XMRIG_HUGEPAGES", "xmrig", Warn, fmt.Sprintf("hugepages %.0f%% (%d/%d)", *p, *s.XMRig.HugepagesAllocated, *s.XMRig.HugepagesTotal), "administrator: reserve hugepages at boot (docs/install.md §6: vm.nr_hugepages, P2Pool light-mode) and restart xmrig")
	default:
		r.add("XMRIG_HUGEPAGES", "xmrig", Pass, "hugepages 100%", "")
	}
}

func (r *runner) localChecks(ctx context.Context) {
	if r.cfg.XMRig.TokenFile == "" {
		r.add("TOKEN_FILE", "moneroid", Skip, "token_file not configured", "")
	} else if _, err := r.cfg.ReadToken(); err != nil {
		r.add("TOKEN_FILE", "moneroid", Fail, "token_file: "+err.Error(), "make the file readable by the operator group, one line, no CR/LF")
	} else {
		r.add("TOKEN_FILE", "moneroid", Pass, "token_file is readable", "")
	}
	jctx, cancel := context.WithTimeout(ctx, status.ReadTimeout)
	defer cancel()
	_, stderr, err := r.run(jctx, "journalctl", "--no-pager", "-n", "1", "-u", r.cfg.Services.P2Pool)
	if err != nil || strings.Contains(string(stderr), "not seeing messages") {
		r.add("JOURNAL_ACCESS", "moneroid", Warn, "journalctl: "+strings.TrimSpace(string(stderr)), "add the operator to group systemd-journal to read service logs (SEC-04: not a mining fault)")
	} else {
		r.add("JOURNAL_ACCESS", "moneroid", Pass, "journalctl can read the unit journal", "")
	}
	if r.snap.HasIssue("CLOCK_UNCERTAIN") {
		r.add("CLOCK", "moneroid", Warn, "a Data API file has a modification time in the future", "check the system clock / NTP")
	} else {
		r.add("CLOCK", "moneroid", Pass, "file timestamps are not in the future", "")
	}
	r.nodeCheck(ctx)
	r.add("CONTROL_ACCESS", "moneroid", Skip, "systemd has no safe dry-run for start/stop authorization", "use sudo or the optional polkit rule; test with moneroid restart xmrig")
}

func i64(v *int64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprint(*v)
}

func (r *runner) nodeCheck(ctx context.Context) {
	if r.cfg.P2Pool.ParamsFile == "" {
		r.add("NODE_RPC", "p2pool", Skip, "params_file not configured", "")
		return
	}
	data, err := os.ReadFile(r.cfg.P2Pool.ParamsFile)
	if err != nil {
		r.add("NODE_RPC", "p2pool", Skip, "params_file: "+err.Error(), "")
		return
	}
	p := node.ReadParams(data)
	c, d := p.Node, p.Dialer()
	res := node.Probe(ctx, node.NewHTTPClient(d), d, c)
	switch {
	case res.Error != "":
		r.add("NODE_RPC", "p2pool", Warn, fmt.Sprintf("node %s:%d: %s", c.Host, c.RPC, res.Error), "check host/rpc-port in p2pool.conf or run moneroid node list")
	case res.Synchronized == nil || !*res.Synchronized:
		r.add("NODE_RPC", "p2pool", Warn, fmt.Sprintf("node %s:%d is not synchronized", c.Host, c.RPC), "wait for the node to sync or pick another with moneroid node list")
	case res.ZMQOpen == nil || !*res.ZMQOpen:
		r.add("NODE_RPC", "p2pool", Warn, fmt.Sprintf("node %s: ZMQ port %d is closed", c.Host, c.ZMQ), "the node must run with --zmq-pub; check zmq-port in p2pool.conf")
	default:
		r.add("NODE_RPC", "p2pool", Pass, fmt.Sprintf("node %s:%d answers in %d ms, synchronized, ZMQ port open", c.Host, c.RPC, *res.LatencyMs), "")
	}
}
