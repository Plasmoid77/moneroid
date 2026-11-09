# Moneroid v1 — specification

Date: 2026-09-11. Revision 0.4 — aligned with the stand results (reports 1–2) and the v1 implementation; later additions (payouts, install script, I2P, colours) are marked with their dates. Differences from 0.2 are listed in the [implementation plan](plan.md), §0.

Project working directory: `/home/plasmoid/Moneroid`.
Source material: [handoff v2](history/handoff-v2.md).
This document refines and in places replaces the decisions proposed in the handoff.
It describes the product; the presence of a requirement does not mean the feature is already implemented or verified by running it — see the [acceptance report](research/acceptance-report-v1.md) for that.

## 1. Purpose and result

Moneroid is a local tool for operating one P2Pool + XMRig pair on a Linux server with systemd. Over SSH the user starts and stops the services, sees state and metrics, opens journals and diagnoses faults. The interactive dashboard is optional for control and never owns the lifetime of mining.

The product ships as one executable `moneroid`, example native configurations, two systemd unit files and documentation. XMRig, P2Pool and the Monero node are installed and updated independently.

Low-maintenance goal: changes to algorithms, consensus and optimizations stay inside the upstream programs. Compatibility with arbitrary future versions and security updates are not guaranteed.

### 1.1. Main user scenario

1. The administrator installs the external programs and gives P2Pool access to a ready Monero node.
2. Installs the configs and the two services by the instructions, grants the operator the needed rights.
3. Runs `moneroid doctor`, then `moneroid start`.
4. Checks `moneroid status`, closes the SSH session.
5. Connects later, opens `moneroid tui`, looks at metrics and journals.
6. Leaves the dashboard; mining continues.
7. Independently updates an upstream binary, restarts the corresponding service and keeps using the same Moneroid.

## 2. Accepted v1 scope

| In | Out |
|---|---|
| Linux x86_64, the system systemd instance | Windows/macOS, other init systems, systemd user units |
| One local pair of services, arbitrary service names | Several stacks at once, managing remote machines |
| CLI and an interactive TUI, English commands and labels | Web/GUI, interface localization |
| `status`, JSON, `start/stop/restart`, `logs`, `doctor`; the optional `moneroid guard` (§7.1, owner decision 2026-11-05) | Any other daemon, supervisor or restart scheduler |
| Native configs and installation by exact instructions (by hand or `install.sh`) | A binary downloader inside the tool, auto-update, building the miners, an install wizard |
| A ready local or remote Monero node; node choice from an editable candidate list by latency (§6.1) | Installing, syncing and managing `monerod`; a built-in node list, background node switching |
| Observing the chosen upstream sidechain | Choosing the chain by profitability, own share/payout logic |
| A short hashrate history in the dashboard's memory; the payout list from the P2Pool journal (§6.2) | A database, persistent history, wallet balance, external pool observers |
| Observing already configured CPU mining | Kernel, MSR, hugepages, CPU affinity, GPU and power tuning inside the tool |

The product and command name is Moneroid / `moneroid`. Documentation is in English; JSON field names and diagnostic codes are in English.

The first launch is agreed with the user: ready configs, systemd templates and exact instructions. An install wizard is postponed.

## 3. Architecture choice

Three options were considered:

| Option | Advantage | Cost | Decision |
|---|---|---|---|
| Go + native interfaces + Bubble Tea | One binary, convenient HTTP/JSON/processes, a full TUI | A few libraries and adapter tests | The main option |
| Bash + systemctl + jq + watch | Small size for a non-interactive shell | Harder input, operation cancelling, safe redraw and partial errors | Only if a full TUI is dropped |
| Adapting Gupax | Integration and a daemon mode already exist | More coupled state and features, a different operating model | Not as a base |

Canonical data flow: `XMRig → Stratum P2Pool → RPC/ZMQ Monero node`. Moneroid takes no part in passing jobs, shares or blocks. [Official Monero guide](https://docs.getmonero.org/interacting/mining/guides/p2pool/xmrig-p2pool/)

```text
CLI ─┐                    ┌─ systemctl / journalctl → systemd → P2Pool, XMRig
     ├─ operations, model ├─ HTTP GET → XMRig API
TUI ─┘                    └─ reading local JSON files → P2Pool Data API
```

### 3.1. Important changes relative to the handoff

- The binary path and the native config path belong to the systemd unit file. They are not duplicated in the Moneroid configuration.
- Moneroid does not implement parsers of the whole P2Pool/XMRig configuration. Their semantics are checked by the upstream programs themselves; start errors are visible through systemd and the journal.
- Starting a service is not declared readiness to mine. A TCP port check does not prove sync or receiving jobs either.
- Changing one service does not trigger hidden control of the other. The order of a joint operation is set by systemd.
- The state of every data source is independent. A file's age and the time it was read successfully are different values.
- The XMRig accepted-jobs counter is not labelled as the number of P2Pool shares or payouts.
- The list of mandatory metrics is limited to what the structured interfaces provide reliably.

### 3.2. Components and boundaries

| Component | Responsible for | Not responsible for |
|---|---|---|
| CLI | Arguments, output, exit codes | API parsing, mining |
| TUI | Keyboard, presenting the snapshot, confirming operations | Collecting metrics itself, managing child miners |
| Collector | Parallel bounded collection, in-memory cache of the last valid reply | Persistent storage, restarts |
| Systemd adapter | Reading properties, commands to strictly named units | Fixing units and configs, managing processes by PID |
| XMRig adapter | Reading the local API, normalizing the confirmed fields | Writing through the API, thread setup |
| P2Pool adapter | Reading a fixed set of Data API files | An HTTP server, consensus, log parsing |
| Health/doctor | Explainable checks with reason codes | Automatic repair, profitability assumptions |

Interfaces exist only at the external dependencies: command runner, HTTP, file reading, clocks. The common model does not depend on TUI libraries or on the full upstream JSON. A DI container, an event bus, plugins and RPC between parts of the application are not needed.

## 4. Platform and dependencies

- Build `linux/amd64`, `CGO_ENABLED=0`; Go and interpreters are not needed at runtime.
- Base systemd target: version 249 or newer; the actual compatibility is confirmed by integration checks. This is the product's support boundary, not a statement about the upstream programs' minimum.
- Initial compiler line — Go 1.27; the exact patch is pinned in the build. [Go release history](https://go.dev/doc/devel/release)
- TUI — `charm.land/bubbletea/v2`, initial verified version 2.0.9. This is major v2: v1 API examples must not be mixed with it. [go.mod](https://github.com/charmbracelet/bubbletea/blob/v2.0.9/go.mod)
- TOML — `github.com/BurntSushi/toml`; colour downsampling — `github.com/charmbracelet/colorprofile` (already in the graph via Bubble Tea). CLI — the standard `flag` and a small dispatcher; Cobra, Bubbles and a separate styling framework are not required.
- Reference upstream versions for the initial verification: P2Pool 4.17.1, XMRig 6.26.0 — the versions whose sources the contracts were checked against. As of 2026-09-11 the current P2Pool release is 4.18 (2026-08-05); the stage-1 stand used the current releases and re-checked the contracts with them. The application does not download versions and keeps no table of allowed versions. Future versions are supported through the interfaces.
- A release contains the binary, checksums, example configs/units and instructions. Distribution packages, containers and automatic release downloads are not part of v1.

## 5. Configuration ownership and layout

### 5.1. Single source of every parameter

| Parameter | Owner |
|---|---|
| Payout address, node RPC/ZMQ, sidechain, Stratum bind, Data API output | native `p2pool.conf` |
| Pool endpoint, CPU profile, HTTP API, donation and the other miner settings | native `xmrig.json` |
| Executable, launch arguments, Unix user, working directory | the systemd unit and its drop-ins |
| Which units to observe/control, where to read the API, refresh rate, `params_file`/`nodes_file` paths | `moneroid.toml` |
| Monero node candidate list | `nodes.txt`; the result of `node select` is written into three keys of `p2pool.conf` — the only write Moneroid makes to a native config (§6.1) |

The observed address must inevitably match the data producer's settings. Moneroid does not synchronize these files automatically. Moving the API changes the corresponding native config and the observed address; moving the binary — only the unit. Editing a unit needs `daemon-reload`, editing a native config — a restart of the upstream service.

### 5.2. Proposed layout

```text
/etc/moneroid/moneroid.toml
/etc/moneroid/p2pool.conf
/etc/moneroid/xmrig.json
/etc/moneroid/xmrig-api.token      # only if the API uses a token
/etc/systemd/system/moneroid-p2pool.service
/etc/systemd/system/moneroid-xmrig.service
/var/lib/moneroid/p2pool/         # cache, peers and other upstream data
/run/moneroid-p2pool-api/         # temporary Data API of the current run
/var/lib/moneroid/xmrig/          # XMRig working directory
```

These are the system paths of an installation. The repository in `/home/plasmoid/Moneroid` is not the services' working directory.

### 5.3. Moneroid configuration schema

```toml
schema_version = 1

[services]
p2pool = "moneroid-p2pool.service"
xmrig = "moneroid-xmrig.service"

[p2pool]
data_api_dir = "/run/moneroid-p2pool-api"
# params_file = "/etc/moneroid/p2pool.conf"   # only for node select and NODE_RPC
# nodes_file  = "/etc/moneroid/nodes.txt"     # only for node list/select

[xmrig]
api_url = "http://127.0.0.1:18088"
expected_id = "moneroid-xmrig"
# token_file = "/etc/moneroid/xmrig-api.token"

[ui]
refresh_ms = 1000
```

CFG-01. `/etc/moneroid/moneroid.toml` is read by default; `--config PATH` selects another file. No search in the cwd, no automatic merging of several configs, no environment substitution. Defaults apply only to omitted optional fields; a missing file is an explicit error.

CFG-02. The own schema is strict: unknown keys, duplicate keys, wrong types and an unsupported `schema_version` are rejected. Paths are absolute; shell expressions are not evaluated. `refresh_ms`: 500–10000 inclusive.

CFG-03. The two unit names differ, end with `.service`, do not start with `-`, contain no glob, spaces, control characters or `/`. For v1 the form `[A-Za-z0-9_][A-Za-z0-9_.@:-]*\.service` is allowed; no pattern search for matching services.

CFG-04. `api_url` is HTTP with a literal loopback IP (`127.0.0.0/8` or `::1`), an explicit port 1–65535, without credentials/query/fragment or an arbitrary path. The client uses no proxy from the environment and follows no redirects. A remote XMRig API is not supported.

CFG-05. `token_file` is optional; its content is read when calling the API, never printed and passed only in the Bearer header. Size is limited to 4 KiB; surrounding newlines are stripped, inner CR/LF are rejected. A read error does not prevent service control.

CFG-06. The configuration is loaded once per CLI/TUI run. After changing the file the dashboard must be reopened. `version`, `--help` and `config path` need no reachable APIs or running services.

CFG-07. `expected_id` is a mandatory non-empty string up to 128 characters without control characters. It equals the native XMRig `api.id` and is checked against the `id` field of the reply. A mismatch is `SOURCE_ID_MISMATCH`, not a proof for health. This guards against accidentally attaching to another instance; it is not authentication.

CFG-08. `params_file` and `nodes_file` are optional absolute paths. Without them the `node` commands end with an argument error and the `NODE_RPC` check is a `skip`. Other commands do not read these fields.

## 6. CLI contract

```text
moneroid [--config PATH] status [--json] [--check]
moneroid [--config PATH] tui
moneroid [--config PATH] start   [all|p2pool|xmrig]
moneroid [--config PATH] stop    [all|p2pool|xmrig]
moneroid [--config PATH] restart [all|p2pool|xmrig]
moneroid [--config PATH] logs [--follow] [--lines N] [all|p2pool|xmrig]
moneroid [--config PATH] doctor [--json]
moneroid [--config PATH] node list [--json]
moneroid [--config PATH] node select [--dry-run]
moneroid [--config PATH] payouts [--json] [--since TIME]
moneroid [--config PATH] config path
moneroid version
moneroid --help
```

CLI-01. A missing target means `all`; an unknown target is an error before systemd is called. Without a subcommand help is printed with code 0. `--config` goes before the subcommand; its local flags after the subcommand and before the positional target. Help shows copy-pasteable examples of this grammar.

CLI-02. `status` does one collection and prints a partial result even when every API fails. After the own configuration is read, a plain `status` exits 0 if all bounded collection attempts finished and the snapshot could be printed, regardless of source states. An internal build/output error is 1. `status --check` returns 0 only with `health.level=ok`, otherwise 1. With `--json --check` the full JSON is still printed.

CLI-03. JSON goes to stdout only, without colour, progress or text prefixes. Diagnostics of the call itself go to stderr. Source fields with errors stay in the snapshot. `status --json`, `doctor --json` and `payouts --json` have separate documented schemas with `schema_version=1`.

CLI-04. `start`/`stop` are idempotent in the systemd sense. Before `start`/`restart` a unit in `Result=start-limit-hit` gets `systemctl reset-failed` (otherwise systemd refuses until the interval ends); the polkit rule includes that verb. `restart` really stops and starts the selected services, including starting a previously stopped selected service. Only `restart all` touches both; `restart p2pool` does not restart XMRig.

CLI-05. After a control operation the systemd result and the observed states of the selected services are printed. An error/timeout does not mask partial success. A successful `start` means the systemd job finished and the selected services are active/running at the control read; it does not mean sync, positive hashrate or a payout.

CLI-06. `logs` prints the last 100 entries of the selected units by default and exits. `--follow` keeps reading; `--lines N` accepts 1–10000. `journalctl` is used with exact `-u`, `--no-pager` and safe text output; no shell is started. `Ctrl-C` ends the view and does not stop the services.

CLI-07. Minimal exit codes:

| Code | Meaning |
|---|---|
| 0 | The command fulfilled its contract |
| 1 | An unmet `--check` condition, doctor failures, or an unsuccessful/partial operation |
| 2 | Argument, syntax or own-configuration value error |
| 3 | A permission refusal that blocks the command itself: reading the own config, control or logs |
| 4 | The control operation timed out; the final state may still be undetermined |
| 130 | The CLI was interrupted with Ctrl-C |

Errors of individual sources in the snapshot do not turn into code 3/4 for a plain `status`. A non-working systemd is also presented as an unavailable source once the configuration is read.

Ctrl-C in `logs --follow` and in `tui` is the normal way out, code 0. Code 130 applies only to interrupted `status`, `doctor` and control operations.

### 6.1. Choosing the Monero node

Accepted by the owner on 2026-09-11 as a replacement for manual node choice; implemented minimally.

NODE-01. Candidates are a text file `nodes_file`: one node per line `host rpc_port zmq_port`, `#` is a comment, empty lines are ignored. The example file ships with community nodes that support ZMQ, with the list source and check date; the owner edits the file. Moneroid has no built-in list and neither downloads nor updates it.

NODE-02. `node list`: for every node, in parallel, JSON-RPC `get_info`, then `get_block_headers_range` for 100 blocks (P2Pool makes this call at start; on filtering networks it breaks while `get_info` works — Zeonux stand 2026-09-14) and a ZMTP handshake with the ZMQ port (a 10-byte signature exchange; an open TCP port does not prove a ZMQ publisher — stand 2026-09-14); each step has a 3 s timeout, HTTP without an environment proxy or redirects, body ≤ 1 MiB, no credentials. If `params_file` sets `socks5 = IP:port`, the probe goes through that SOCKS5 proxy — as P2Pool does, including its exception: loopback/private/link-local addresses connect directly (`is_private_address` in P2Pool; needed for Tor/I2P with a local node). The name is resolved locally, the addresses are tried in turn (IPv4 first), each with the full check; `select` writes the address that passed (P2Pool takes the first DNS answer). Printed: latency, `synchronized`, `height`, `headers_ok`, ZMQ availability, error. Ranking: only `synchronized=true` with `headers_ok` and available ZMQ, by ascending latency. The command writes nothing; `--json` is a stable schema with `schema_version=1`.

NODE-03. `node select`: runs NODE-02, takes the first ranked node and rewrites in `params_file` exactly three keys `host`, `rpc-port`, `zmq-port`: existing lines of these keys are replaced, absent ones appended; the other bytes of the file are preserved. The write is atomic — a temporary file in the same directory, owner/mode preserved, rename. Write permission is needed — usually `sudo moneroid node select`. `--dry-run` prints the choice and the lines to be replaced without writing. After writing a reminder about `restart p2pool` is printed; no automatic restart. No candidates → code 1; no permission → code 3.

NODE-04. Not in v1: node choice on `start`, background switching, TUI integration, RPC with login/password, TLS.

NODE-05. A remote node learns the host's IP and the payout address (P2Pool sends it in `get_block_template`). The documentation says so explicitly and calls a local node the more private option.

### 6.2. Payouts

Accepted by the owner on 2026-09-16. The only place where Moneroid reads journal lines: P2Pool reports payouts only in its log, they are events, not metrics; the rule "the journal is not a metrics source" (§3.1) stands.

PAY-01. `payouts` asks journald only for the matching lines of the P2Pool unit: `journalctl -o short-iso -u UNIT -g 'got a payout of|didn't get a payout in block' [--since TIME]`. The line format is fixed by the P2Pool 4.18 sources (`src/p2pool.cpp`): `Your wallet <address> got a payout of <integer>.<12 digits> XMR in block <height>`. The amount is parsed as an integer in atomic units.

PAY-02. Output (CLI and the dashboard on `p`): time in the system's local zone, XMR amount, block height, total; separately the number of pool blocks found without a share of yours in the PPLNS window. The wallet address never reaches the output or JSON (SEC-07). `--json`: `schema_version=1`, `payouts[]{at, atomic_units, xmr, block}`, `total_atomic_units`, `total_xmr`, `blocks_without_payout`.

PAY-03. No matches is not an error (journalctl returns 1 without stderr). A journal access refusal is code 3: journalctl is run without `-q`, so it reports on stderr that the caller is not seeing system messages, and the TUI shows that error instead of "no payouts". A changed line format in future P2Pool versions yields an empty list, not false amounts; checked at upstream updates (A20). The source of truth about money is the wallet; external observers (`p2pool.observer`) are not used: they link the observer's IP with the payout address.

## 7. systemd and lifecycle

SYS-01. The services run the upstream binaries directly with their native configs. Moneroid is not used in `ExecStart`, `ExecStartPre`, `ExecStartPost` or a watchdog. After removing the Moneroid binary the configured services keep working.

SYS-02. In the shipped units: `Type=exec`, `Restart=on-failure`, `RestartSec=5s`, `StartLimitIntervalSec=60s`, `StartLimitBurst=5`, `TimeoutStopSec=30s`, `KillMode=control-group`, output to journald, `StandardInput=null`. Clean termination on SIGTERM is verified separately on both upstream programs.

SYS-03. The XMRig unit contains `After=moneroid-p2pool.service`. There is no `Wants`, `Requires`, `BindsTo`, `PartOf` or a shared user `.target` between the two services. Both have `WantedBy=multi-user.target` and are enabled for autostart explicitly. `After` orders only when both jobs exist: P2Pool starts before XMRig, stops in reverse order. [systemd.unit](https://github.com/systemd/systemd/blob/v261/man/systemd.unit.xml)

SYS-04. A joint operation passes both exact unit names to one `systemctl` call; a single one — one unit. Ordering is systemd's, and XMRig reconnects to P2Pool on its own. Stratum readiness and sync are not part of the start condition.

A joint operation is not atomic: a partial result is possible. No rollback is performed; the final state is read even on nonzero/timeout.

SYS-05. `Type=exec` shows an execution error of the binary but not the end of its initialization. Therefore Moneroid never prints `P2Pool synced` or `Mining ready` after a successful `systemctl start`. [systemd.service](https://github.com/systemd/systemd/blob/v261/man/systemd.service.xml)

SYS-06. State is read through `systemctl show --all --property=...`, not by parsing the localized `systemctl status`. Needed: `Id`, `LoadState`, `ActiveState`, `SubState`, `UnitFileState`, `Result`, `MainPID`, `ExecMainStatus`, `NRestarts`, `InvocationID`, `ActiveEnterTimestampMonotonic`, `InactiveExitTimestampMonotonic`. Missing properties are allowed and shown as unknown.

For matching process time `ExecMainStartTimestamp` and `ExecMainStartTimestampMonotonic` are read as well. Doctor separately asks for the effective `After`, `Wants`, `Requires`, `BindsTo`, `PartOf`, `RuntimeDirectory` and `RuntimeDirectoryPreserve`, honouring drop-ins and the actual `Id`.

SYS-07. Commands run as argument arrays through `os/exec`, with the pager and interactive authorization prompts disabled. No `sh -c`, `eval`, arbitrary executable/command prefixes from TOML or PID management by process name.

Normative control form: `systemctl --no-pager --no-ask-password VERB -- UNIT...`.

SYS-08. The control operation deadline is 90 seconds, the subsequent state read up to 3 seconds. On cancel only the systemctl client ends; a job already handed to systemd may continue. Moneroid says so and suggests checking the actual status. No automatic rollback or compensating stop/restart.

SYS-09. At most one control operation runs inside one Moneroid process at a time; a repeated key press in the TUI creates no duplicate. Between independent processes systemd orders the commands. A job cancelled/replaced by an external command shows as a result conflict, without an own lock daemon.

SYS-10. Foreign units keep their existing dependency semantics. The exact guarantees of SYS-03/04 hold for the shipped units; `doctor` warns about differing dependencies and a missing `After`. Moneroid does not rewrite foreign units.

SYS-11. The P2Pool unit sets `RuntimeDirectory=moneroid-p2pool-api`, `RuntimeDirectoryPreserve=no`, `RuntimeDirectoryMode=0750`; the persistent directories are `StateDirectory=moneroid/p2pool` and `moneroid/xmrig` (0700). Systemd creates the temporary directory at start and removes it at stop; the persistent upstream cache/peers stay in `/var/lib/moneroid/p2pool`. For foreign units without this profile the freshness and the link of the files to the current process need a separate check; no proof means unknown.

### 7.1. Guard

Owner decision 2026-11-05: P2Pool can come up on its own isolated sidechain (after a restart with an unreachable node or a stale cache) and XMRig then hashes for nothing, silently. The guard is the one sanctioned exception to "no daemon" (§2). It never replaces systemd: it only stops/starts the two units and, rarely, moves P2Pool's cache aside.

GUARD-01. `moneroid guard` is a long-running loop run by `moneroid-guard.service` (root, `CapabilityBoundingSet=CAP_DAC_OVERRIDE`, `ProtectSystem=strict`, `ReadWritePaths=-/var/lib/moneroid/p2pool`, `RuntimeDirectory=moneroid-guard` 0775 group `moneroid` with `RuntimeDirectoryPreserve=restart`, `StateDirectory=moneroid/guard`). Optional: without the unit Moneroid behaves as before. `install.sh` installs the unit always and enables/starts it with `--enable`; `--uninstall` stops it first. A timer + oneshot was rejected: the hysteresis counters live between polls, and a loop with `Restart=on-failure` is the simpler mechanism.

GUARD-02. Every `[guard] interval_s` (default 30) it builds the same snapshot as `status` and classifies P2Pool alone, independently of XMRig's state: `Skip` (P2Pool unit not active or systemd unreadable — no decision), `Ready`, or `NotReady` with the first failing reason in this order: `SOURCE_STALE`, `P2P_UNTRUSTED` (local/p2p missing/invalid, from a previous run, unknown session or clock), `P2P_NO_CONNECTIONS`, `ZMQ_ACTIVITY_OLD`, `POOL_STATS_UNKNOWN` (pool/stats missing or older than 180 s), `PEERS_UNKNOWN`, `SIDECHAIN_BEHIND` (highest connected peer more than 100 blocks ahead).

GUARD-03. Hysteresis: `NotReady` for `hold_after` (3) consecutive polls while XMRig is active → hold: write the marker, `systemctl stop` XMRig. `Ready` for `release_after` (3) consecutive polls with a marker present → `systemctl start` XMRig, remove the marker. A `Ready` poll counts only when local/p2p was rewritten since the previous counted poll; `NotReady` counts every poll. Exposure at boot (both units start together) is bounded by `hold_after × interval_s` (~90 s) — accepted.

GUARD-04. The marker `/run/moneroid-guard/held` (`reason=CODE since=RFC3339`) is the only hold state. The guard starts XMRig only while the marker exists, and creates it only for an XMRig that is running (an XMRig the user stopped is never adopted, not even by a repair). `moneroid start|stop|restart` and the TUI remove it whenever they act on XMRig, so the guard never undoes a user decision; a bare `systemctl stop moneroid-xmrig` does not — stop the guard too, or use `moneroid stop`. Both sides serialize on `flock(/run/moneroid-guard/lock)` (created by the guard; regular files only, no symlinks), and the guard re-reads the live unit state and the marker under the lock before every action. The marker and the state file are written through exclusively created temporary files. Locking fails closed: the guard skips an action it cannot lock, and the CLI/TUI refuse an XMRig operation whose hold they cannot clear. Known limit: a manual `moneroid stop p2pool` while a repair waits to confirm P2Pool running is undone by the next poll.

GUARD-05. `status`, `tui` and `doctor` read the marker: with P2Pool active and XMRig inactive they report `GUARD_HOLD` (warning) instead of `UNIT_NOT_ACTIVE`, plus the P2Pool runtime issues that explain the hold; JSON gains `guard {held, reason, since}` (field added, schema_version unchanged).

GUARD-06. Repair (`[guard] repair`, default on) runs only when all hold: reason `SIDECHAIN_BEHIND` continuously for `repair_after_s` (1200), P2Pool uptime ≥ `repair_after_s`, at least 3 connected peers ahead, the gap not shrinking since the incident began, no repair yet in this incident (it ends at the first `Ready`), and `repair_cooldown_s` (3600) since the last attempt (persisted in `/var/lib/moneroid/guard/last-repair`). Node, ZMQ and peer failures never trigger it — a cache reset cannot fix them. Sequence: record `<time> pending`, hold XMRig, stop P2Pool, confirm it is inactive (otherwise abort, nothing moved), rename `p2pool.cache` and `p2pool_peers.txt` to `*.guard-old` (one previous copy), start P2Pool and confirm it is active, record `<time> done`. Until that confirmation the record stays `pending` and every poll (also after a guard restart) starts P2Pool again. The record also keeps `open|closed`: whether the current incident already had its repair, so a guard restart grants no second one. XMRig stays held until GUARD-03 releases it; a failure to hold it aborts the repair before P2Pool is touched.

GUARD-07. Repair is refused at guard start (logged, hold logic stays) unless `params_file` sets `data-dir = /var/lib/moneroid/p2pool`, the one writable path of the unit. File operations go through `os.Root` on that directory, so symlinks cannot redirect them.

GUARD-08. The guard logs one line per verdict change and per action to the journal (`moneroid logs` does not include it: `journalctl -u moneroid-guard`), never one per poll.

## 8. Permissions and safe operation

SEC-01. The CLI status and the TUI run as a regular user. In the base installation P2Pool and XMRig have separate system accounts without interactive login. Configs and units belong to root and are not writable by the services.

SEC-02. One group `moneroid` for operators: reading the Data API, reading the native configs and (through the optional polkit rule) controlling the two units. The observers/operators split was dropped after the stand: with stdin not a tty P2Pool publishes in `local/console` the port and cookie of its TCP console, so read access to the API directory equals control of P2Pool. The API directory is the `RuntimeDirectory` of the P2Pool unit with `User=moneroid-p2pool`, `Group=moneroid`, `RuntimeDirectoryMode=0750`, `UMask=0027`: subdirectories 0750, files 0640 (verified; no setgid needed). Configs are `root:moneroid 0640`; the services' `StateDirectory` is 0700. The operator gets no write access to the API or the configs.

SEC-03. Control uses the existing systemd/polkit rights; the TUI has no password prompt. Without rights the operation is refused and observation continues. The administrator may run the CLI through sudo outside the TUI or install the optional polkit rule: only `manage-units`, only the two selected exact names and only `start/stop/restart` (plus `reset-failed`), only the explicit operator group. The rule and the group creation are a separate administrative installation step.

SEC-04. Suggesting NOPASSWD for all of `moneroid`, arbitrary `systemctl` or user-editable units is forbidden. Journal reading is governed by the system separately; lacking that right does not mean mining is broken.

SEC-05. The XMRig API in the examples is bound to loopback and runs in restricted/read-only mode. Moneroid does GET only and does not use the control API. A local token is supported, is not passed in argv and never reaches the snapshot, the journal or an HTTP error message.

The base example needs no token. When enabled, the file is `root:moneroid 0640`, the value equals `http.access-token` of the native config; rotation updates both values and restarts XMRig. HTTP 401/403 is shown as an authorization error, not a stopped process.

SEC-06. Stratum listens on loopback by default. The Data API is not published over HTTP. Opening ports, changing the firewall, UPnP and system settings are not done by Moneroid. The P2Pool example explicitly disables UPnP.

SEC-07. The payout address stays in the native config and is not shown by default in status/JSON. Seeds, private keys and wallet passwords are never requested. Endpoint strings and errors have URL userinfo and tokens removed before output; configs are not copied wholesale into reports.

SEC-08. The conservative unit profile uses `NoNewPrivileges=yes`, `PrivateTmp=yes`, `ProtectSystem=strict`, `ProtectHome=yes` and explicitly allowed state directories. Options incompatible with RandomX/JIT are not enabled: in particular `MemoryDenyWriteExecute=yes` is not allowed. Binaries live outside home. Host MSR/hugepage tuning is a separate administrative procedure; Moneroid neither requires it nor promises maximum hashrate without it. A consequence of the unprivileged `User=`: XMRig cannot apply its own MSR mod, and that hashrate penalty is a deliberate choice of the profile. The instructions say so explicitly and describe MSR as a separate administrative procedure (the optional oneshot unit), not a change to the XMRig unit.

## 9. Data collection, errors and freshness

DATA-01. One collection reads systemd, XMRig and P2Pool independently. A failure of one source does not cancel the others. The collection deadline is 3 seconds; an HTTP/systemctl read is 1 second per attempt. In the normal TUI a new collection does not start until the previous one finished; skipped ticks do not accumulate.

DATA-02. Only the known P2Pool files are read: `local/stratum`, `local/p2p`, `network/stats`, `pool/stats`. `pool/blocks` and the payout history are not needed for v1. The limit for each file and HTTP body is 1 MiB; the stderr/stdout volume of short diagnostic subprocesses is limited too. A source must be a regular file on a local file system, not a FIFO/device. A network file system for the Data API is not supported.

DATA-03. Unknown upstream JSON fields are ignored. A missing/null/wrongly typed optional field becomes `null` and a local unavailability reason, without destroying the neighbouring metrics. Corrupted JSON and a non-object top level make the whole source unavailable. Numbers keep counter precision, NaN/Infinity are rejected, negative counts/rates are invalid.

DATA-04. For a vanished/empty/corrupted P2Pool file one retry after 50 ms within the deadline is allowed. No retry for permission denied and size overflow. HTTP is not retried within one cycle.

DATA-05. The last valid reply is kept only in memory of the current CLI/TUI session. On a failed refresh it may be shown with an explicit stale/error mark and the original time; the last success time is not updated. A separate new `status` has no memory of previous runs.

DATA-06. For every source `observed_at`, `last_success_at`, `source_updated_at`, `age_seconds`, `state` and `error_code` are kept separately. For HTTP `source_updated_at=null`; the time of a successful HTTP reply is the observation time. For files the age comes from the producer's update time/mtime, not from a successful re-read of old JSON.

DATA-07. P2Pool sources have different write frequencies. One stale threshold for all files is forbidden. The event-driven `network/stats`, `pool/stats` and `local/stratum` show their age; a missing event is not an error by itself. The periodic source is `local/p2p` (~60 s); the product's stale threshold is 180 s.

DATA-08. On a changed `InvocationID`, a decreased uptime or a service passing through a stop, its data cache is dropped and the chart gets a gap. Files from a previous run found before the first fresh write of the current process do not confirm the current working state. If the source identity is not proven, that limitation is reflected in the diagnostics.

For a stock P2Pool the link is supported by the unit-owned temporary directory and by the check that the uptime from local/p2p plus the file age does not exceed the process uptime by more than 5 s. For XMRig the expected_id and a similar uptime consistency are checked. Comparing mtime with the start uses a wall-clock timestamp; uptime uses monotonic clocks. Clock incompatibility or an impossible match gives `SOURCE_SESSION_UNKNOWN`. Never compare a wall-clock mtime directly with a monotonic timestamp.

DATA-09. A new chart point is added only for a fresh value. History length is 10 minutes, the limit 1200 points; a missing hashrate leaves a gap, a real zero is drawn as zero. After leaving the TUI the history is gone.

## 10. Snapshot and the meaning of metrics

The normative list of metrics, upstream JSON fields and units is in the [source contracts](research/upstream-contracts.md). In the user model the units are fixed: hashrate in H/s, time in seconds or RFC3339 UTC, difficulty a dimensionless number, counters integers, percentages explicitly named `_percent` fields. `null` means absence of knowledge, 0 a measured zero.

`MiningSnapshot` contains `schema_version`, `collected_at`, `collection_duration_ms`, `services`, `sources`, `xmrig`, `p2pool`, `health`. Objects and their fields are present even with unavailable metrics; collections without elements are `[]`, unknown values are `null`.

For every service: `unit`, `load_state`, `active_state`, `sub_state`, `enabled_state`, `pid`, `invocation_id`, `uptime_seconds`, `restart_count`, `last_exit_status`, `result`. `enabled_state` is not mixed with `active_state`.

For every error/warning: a stable `code`, `severity` (`info|warning|error`), `component`, a short `message`, `source`. The human-readable text may be refined without a schema change; automation must use the code.

MET-01. The XMRig hashrate and the local P2Pool hashrate are shown separately: they are different estimates with different averaging windows.

MET-02. XMRig accepted/rejected submissions and P2Pool sidechain shares have different labels and fields. None of these counters means a received payout or the number of Monero blocks.

MET-03. The base interface never prints `synced=yes` from a file's presence, an open port, the number of peers or a positive hashrate. Without a direct reliable sign — `sync: unknown` or no such line.

MET-04. `Huge Pages` and `MSR` are shown only with a confirmed structured metric of the corresponding upstream version. `/proc/meminfo` can confirm that the host has hugepages allocated but not their use by the specific miner. An unproven state is `unknown`.

MET-05. The P2Pool network height is not called the proven height of a synchronized Monero node. A sidechain without a proven source is not guessed from the port.

## 11. Health and doctor

H-01. The aggregated health describes the observed state of the components, not consensus correctness, profitability or a payout guarantee. The interface always lets the user see the underlying service/API states and reasons.

H-02. `health.level`: `ok`, `degraded`, `stopped`, `starting`, `unknown`. Rules apply in order: (1) unit not-found/error/bad-setting or active_state=failed → degraded; (2) both loaded/inactive → stopped, their runtime problems do not affect health; (3) any activating/deactivating → starting; (4) one active, the other inactive → degraded; (5) service states/required data unavailable or not matched to the instance → unknown; (6) the remaining cases are decided by H-03. An old Result and unavailable APIs of stopped services do not cancel stopped. Masked is shown separately and forbids the expected start, but by itself does not turn active into inactive.

H-03. `ok` requires both services active/running, a successful current XMRig summary with a matching ID, a fresh local/p2p of the current run, `xmrig.hashrate_10s_hs>0`, `connection.uptime_ms>0`, P2P connections>0, the local sidechain height not more than 100 blocks behind the peers (`SIDECHAIN_BEHIND` — shares on a lagging/isolated chain are not paid) and an estimated ZMQ age ≤ 600 s. A known violation of these conditions → degraded, a missing required field → unknown. Event files are not required for ok and do not confirm sync. A long ZMQ pause is a warning about observed activity, not a proven network break.

H-04. An old non-zero rejected counter creates no eternal warning. A one-off status shows the values; a growth warning is possible only in the TUI when two fresh samples of one session show an increase. No automatic conclusion about the reject cause. A zero sidechain share count is not an error by itself either.

DOC-01. `doctor` only reads: the own config, systemd/unit availability, their properties/dependencies, API availability and freshness, the needed permissions. It does not run a binary's `--version` from an arbitrary path, does not start services, does not write configs, does not touch kernel/firewall.

DOC-02. Checks return `pass|warn|fail|skip`, a code, the actual observation and the recommended manual action. `doctor` exits 1 when there is a fail; warn does not change the exit code. Stopped services get warn, checks of their runtime API skip, missing/wrongly loaded units fail.

DOC-03. A full pre-check of the native configs and RPC/ZMQ is not promised. Duplicating node/Stratum parameters for doctor is not required. If confirmed P2Pool data points to an upstream node problem, doctor shows that fact and points to the journal. An own Monero address validator and a network crawler are not in v1; RPC is limited to `get_info`/headers for NODE-02 and the `NODE_RPC` check.

DOC-04. Consequences are not presented as causes: `API unavailable` ≠ `process crashed`; `P2Pool active` ≠ `node synchronized`; `unit disabled` means no configured autostart, not a stopped running process.

DOC-05. v1 check catalogue. An own-configuration error ends `doctor` with code 2 before the checks. `skip` means the check makes no sense in the observed state (e.g. the runtime API of a stopped service).

| Check code | Component | Results | Condition |
|---|---|---|---|
| `SYSTEMD_AVAILABLE` | systemd | pass/fail | `systemctl show` answers within the deadline |
| `UNIT_LOADED` | p2pool, xmrig | pass/fail | `LoadState=loaded`; not-found/error/bad-setting → fail |
| `UNIT_MASKED` | p2pool, xmrig | pass/fail | not masked |
| `UNIT_ENABLED` | p2pool, xmrig | pass/warn | `UnitFileState` enabled; otherwise warn with `UNIT_DISABLED` |
| `UNIT_ACTIVE` | p2pool, xmrig | pass/warn/fail | active → pass; inactive → warn; failed → fail with `Result`, `ExecMainStatus`, `NRestarts` |
| `UNIT_ORDERING` | xmrig | pass/warn | the effective `After` contains the P2Pool unit |
| `UNIT_DEPENDENCIES` | p2pool, xmrig | pass/warn | no `Wants/Requires/BindsTo/PartOf` between the units; otherwise `DEPENDENCIES_DIFFER` |
| `UNIT_RUNTIME_DIR` | p2pool | pass/warn | `RuntimeDirectory` matches `data_api_dir`, `RuntimeDirectoryPreserve=no`; otherwise the link of the files to the process is unprovable |
| `DATA_API_DIR` | p2pool | pass/fail/skip | a regular directory on a local FS, readable; permission denied → fail; unit inactive → skip |
| `DATA_API_P2P` | p2pool | pass/warn/skip | `local/p2p` valid, not stale, membership in the current run not refuted |
| `DATA_API_EVENT_FILES` | p2pool | pass/warn/skip | `local/stratum`, `network/stats`, `pool/stats` readable and valid; the age is shown and does not affect the result |
| `P2P_CONNECTIONS` | p2pool | pass/warn/skip | connections > 0 |
| `SIDECHAIN_SYNC` | p2pool | pass/warn/skip | the local sidechain height is not more than 100 blocks behind the maximum height of the connected peers; after start P2Pool downloads and verifies the PPLNS window for minutes and before that logs `SYNCHRONIZED` on its own empty chain (Zeonux stand 2026-09-16) |
| `ZMQ_ACTIVITY` | p2pool | pass/warn/skip | estimated ZMQ age ≤ 600 s |
| `XMRIG_API` | xmrig | pass/fail/skip | HTTP 200 and valid JSON; 401/403 → fail `PERMISSION_DENIED`; unreachable → fail `SOURCE_UNAVAILABLE` |
| `XMRIG_ID` | xmrig | pass/fail/skip | `id` equals `expected_id` |
| `XMRIG_SESSION` | xmrig | pass/warn/skip | the API uptime agrees with `ExecMainStartTimestampMonotonic` (5 s tolerance); incomparable → warn `SOURCE_SESSION_UNKNOWN` |
| `XMRIG_CONNECTED` | xmrig | pass/warn/skip | `connection.uptime_ms > 0` |
| `XMRIG_HASHRATE` | xmrig | pass/warn/skip | `hashrate_10s_hs > 0`; null → skip |
| `XMRIG_HUGEPAGES` | xmrig | pass/warn/skip | `hugepages_percent = 100`; null → skip; remedy — administrative hugepages setup |
| `NODE_RPC` | p2pool | pass/warn/skip | the node from `params_file` (`host`, `rpc-port`, `zmq-port`) answers `get_info`, `synchronized=true`, the ZMQ port is open; `params_file` not set → skip |
| `TOKEN_FILE` | moneroid | pass/fail/skip | if set: readable, ≤ 4 KiB, no inner CR/LF; not set → skip |
| `JOURNAL_ACCESS` | moneroid | pass/warn | `journalctl --no-pager -n 1 -u UNIT` ends without a permission refusal; refusal → warn (SEC-04) |
| `CLOCK` | moneroid | pass/warn/skip | no API files with an mtime more than 5 s in the future |

The right to control operations is not checked: systemd has no safe check without performing the operation. `doctor` says so in one `skip` line with the code `CONTROL_ACCESS`.

## 12. TUI

UI-01. One main screen filling the terminal height in the spirit of `htop`: a title bar (version, host, time, health), service states, XMRig hashrate of 3 windows with a meter relative to the 10-minute maximum, submissions, hugepages, a sparkline of the 10 s hashrate over 10 minutes scaled to its own range (at least the top 10 %, so noise does not fill it; a fall to zero shows), set off by a blank line when the terminal has room, the available P2Pool metrics (including a sidechain sync meter relative to the peers and a pool share estimate), a payouts line from the journal (count, total, last; re-read once a minute), the list of significant issues, data age, a key bar at the bottom. The dashboard uses the same collector and operations as the CLI.

UI-02. Keys: `q`/`Ctrl-C` — leave the dashboard; `r` — refresh after the current collection ends; `s` — services menu; `l` — choose a journal; `p` — payouts (§6.2); `?` — help; `Esc` — close the current dialog. Time in the dashboard is the system's local zone. Leaving never calls stop under any circumstances.

UI-03. In the menu the target is chosen first, then the action. Stop/restart needs a separate confirmation naming the selected services; Cancel is highlighted by default. During an operation progress is visible, resubmission is disabled, rendering and exit stay responsive.

UI-04. For journals the TUI releases the terminal, runs a `journalctl` view, then restores the screen and refreshes the state. Ctrl-C in the view returns to the dashboard. A separate built-in log viewer and log analysis are not needed.

UI-05. The minimal full layout is 80×24. Below that a compact service state and a hint to enlarge the terminal are shown; the exit keys work. Resize causes no panic/unbounded allocation. Without a TTY the `tui` command ends with a clear argument/environment error without control escape sequences.

UI-06. The palette is the Monero colours: orange `#FF6600`, exactly the fill of the official logo (`monero-site/img/logo-list.svg`; owner decision 2026-11-09, 256-colour terminals show the nearest 202; headings, meters, normal states), white (key values), red only for degraded/failed/rejected/errors; the same palette in the text `status` when printing to a terminal (`internal/ansi`; downsampling to 256/16 colours and disabling for pipes and `NO_COLOR` — `colorprofile`). Colour never carries the only meaning: there are text states, `NO_COLOR` and an ASCII fallback are supported. On the Linux console (`TERM=linux`) the TUI and the text `status` print only characters of the loaded console font, read from the kernel (`GIO_UNIMAP`; the built-in CP437 font when it cannot be read): a missing `€` becomes `EUR`, `—` `-`, `…` `...`, anything else without a stand-in `?`, and without eighth blocks the sparkline is a thin trace of `_ ─ ¯` (solid shades would merge with a full meter on the row above, console fonts have no gap between rows). Anything outside the font would show as a square. With console-setup's default Lat15 font `€`, `—` and `…` are drawn as they are. Control characters from upstream strings are not executed by the terminal. The interface restores the terminal after a normal exit, errors and the supported signals, and erases the screen explicitly on exit so terminals without an alternate screen (Linux console) are left clean.

UI-07. Earnings block on the dashboard (owner decision 2026-11-09), three lines under `EARNINGS`: the XMR rate (PRICE-01) with source and age; paid — payouts from PAY-01 with their total and the last payout, in XMR and ≈ USD · EUR at today's rate; expected — XMR a day and a month for the XMRig 15 m hashrate (60 s as fallback) with the same fiat conversion. The payouts screen (`p`) adds a "≈ at today's rate" column. Without a rate only XMR is shown.

UI-08. Pings on the dashboard (owner decision 2026-11-09, as in Gupax). A `node` line under `P2POOL`: the Monero node from P2Pool's config (re-read every time), one `get_info` round trip every minute by the same path P2Pool uses (its `socks5`, the private-address bypass), with the latency (and the median of the last 5, since a single slow answer over a VPN is common), sync state, height and age; no header-range or ZMQ check here (that is `doctor`/`moneroid node`; ZMQ activity is already on the `P2POOL` line), so the traffic stays small on a metered uplink. A `PEERS` block: the connected peers from `local/p2p` (`direction,connected s,ping ms,software,height,address`, ping -1 = not measured yet, P2Pool `p2p_server.cpp` 4.18.1) sorted by ping, unmeasured last, with min/median/max; it gets only the rows left after the other blocks, warnings and issues, the fastest first. Peer strings are sanitized like every upstream string (A18); the list is not in `status --json`.

PRICE-01. Rate sources (owner decision 2026-11-10: a second, non-exchange source): CoinGecko `GET /api/v3/simple/price?ids=monero&vs_currencies=usd,eur`, and if it fails, CoinPaprika `GET /v1/tickers/xmr-monero?quotes=USD,EUR` — one of the two providers of the official Monero GUI wallet (the other is the Kraken exchange). Both are public, without a key, and give both currencies in one answer; an answer whose own update time (`last_updated_at`, `last_updated`) is more than an hour old counts as failed, so a provider that stopped updating cannot pass for fresh; the screen names the source of the rate shown, and when both fail the error lists both reasons. They are asked by the TUI at start, then 10 minutes after a good answer or 1 minute after a failed one, at most one request in flight; a failed request keeps the last rate and shows it as stale, with a one-line reason ("no answer in time", the HTTP code) instead of the Go error with its URL. Only the TUI asks; `status`, `doctor`, the guard and JSON never touch the network for it. `[ui] price = false` turns the request and the display off. The request carries no address or host data; the rate is display-only and never feeds a decision.

PRICE-02. The request goes through P2Pool's `socks5` when one is set in the params file (as the node probes, NODE), otherwise directly; no environment proxy, no redirects, 64 KiB answer limit; non-positive or missing values are dropped. Each request has its own 10 s timeout, not the node probes' 3 s: through a VPN on a mobile uplink CoinGecko often answers in 3–5 s.

PRICE-03. Expectation: `XMR/day = hashrate × 86400 / network_difficulty × block_reward` with the P2Pool `network/stats` difficulty and `reward` (`network_reward_atomic` in the status JSON). It is the long-run mean that P2Pool payouts scatter around, not a forecast; labelled "expected".

## 13. Installation and documentation

INSTALL-01. The distribution contains verified examples of the native configs and units. Installation is described step by step with explicit administrative commands for creating users/directories, setting permissions, installing files, `daemon-reload`, granting the optional operator rights and enabling autostart; the same steps are performed by `install.sh` (POSIX sh, idempotent, as a user with sudo): the newest stable official P2Pool/XMRig releases and Moneroid release (or the versions given with `--p2pool-version`/`--xmrig-version`/`--moneroid-version`), verified per INSTALL-06 (a cache of verified downloads in `/var/cache/moneroid`), the operator added to the groups `moneroid` and `systemd-journal`, wallet and sidechain substitution, automatic node choice, optionally autostart, hugepages/MSR and I2P (`--i2p`: i2pd, a server tunnel, the P2Pool `socks5`/`i2p-address` parameters). The Moneroid binary itself downloads and installs nothing. The example must not start mining without the user's address and node substituted.

INSTALL-02. The native P2Pool params-file is passed as the single argument pair `--params-file PATH`; additional runtime settings live in the file itself. XMRig gets `--config PATH`. Syntax, disabling coloured/duplicate file logging, stdin=null and a read-only config are verified on the reference versions.

INSTALL-03. The examples do not hide the upstream donation and CPU settings; the safe integration changes are listed explicitly. For XMRig automatic writing to the root-owned config is disabled if upstream enables it by default. P2Pool main/mini/nano is chosen by the user in the native config; the example's default is mini, without a claim of optimal profitability.

INSTALL-04. The README contains: purpose, support boundaries, prerequisites, installation, the first-run scenario, permissions and the full CLI. `docs/troubleshooting.md` and `docs/updating.md` ship separately. Updating along stable paths does not require changing Moneroid; moving the executable requires changing the unit.

INSTALL-06. Download integrity (owner decision 2026-11-09: always the newest releases, no version pins in the script). The trust anchors in `install.sh` are the primary-key fingerprints of the upstream release signers: P2Pool `1FCAAB4D3DC3310D16CBD508C47F82B54DA87ADF` (SChernykh), XMRig `9AC4CEA8E66E35A5C7CDDC1B446A53638BE94409`. Their public keys ship in `keys/` of the Moneroid release. P2Pool's `sha256sums.txt.asc` (clearsigned) and XMRig's `SHA256SUMS` + `SHA256SUMS.sig` must carry a valid signature by exactly that key (gpg `VALIDSIG`); the archive SHA256 is taken from the signed text only. The Moneroid binary and extras are checked against `SHA256SUMS` of their release, which has the same trust as the script itself (this repository). Any failure stops the install before anything is replaced. A new signing key upstream is a deliberate change of `install.sh` and `keys/`. Without an explicit version the script never installs a release lower than the installed binary (a repository taken over without the signing key could mark an old signed release as latest); revoked or expired signing keys are refused; cached files are used only from a root-owned, not group/other-writable cache whose every parent directory is the same (so nobody else can swap a directory on the path); every argument that reaches a sed program or a config file must match its pattern as a whole, a value with a newline is refused; whatever can be refused (versions, signatures, the `--i2p` node check) is refused before the first installed file is replaced. Known limit: Moneroid's own `SHA256SUMS` is unsigned, so control of this GitHub repository equals root on hosts that run `install.sh` — the same as for the script itself; the owner accepted this instead of maintaining a signing key.

INSTALL-05. The license of the own project is chosen by the owner before the public release (MIT). Studying the upstream interfaces does not include copying GPL implementations. External sources and binaries are not part of the distribution. This item is not a legal opinion on licensing.

## 14. Acceptance checks

Regular unit tests do not call the real systemctl, do not start miners and do not use the internet. JSON fixtures come from the pinned upstream source or are explicitly marked as synthetic broken variants. Real captures are cleaned of tokens, payout addresses and user data.

| ID | Scenario | Acceptance criterion |
|---|---|---|
| A01 | Start, close SSH, reconnect | Both services keep running independently of Moneroid |
| A02 | TUI exit/signal/crash | Not a single stop/kill command sent to the services |
| A03 | XMRig active, HTTP absent | Partial status, service active, API unavailable, metrics unknown |
| A04 | P2Pool stopped, old API files remain | Old values do not confirm the current working state |
| A05 | Write/replace/absence/corruption of one API file | Bounded retry, the other sources independent, an explicit error |
| A06 | A JSON field added/removed/null/wrong type | The whole snapshot does not fail; the loss affects only that metric |
| A07 | Zero hashrate and missing hashrate | 0 and null distinguishable in CLI/JSON/chart |
| A08 | New InvocationID or uptime reset | The old cache and chart are not mixed with the new session |
| A09 | `start/stop/restart all` and single targets | systemd order verified, a single operation does not touch the other stock unit |
| A10 | Timeout/Ctrl-C/an external counter-command during control | No rollback; uncertainty or a partial result shown |
| A11 | No full rights for control/journal/API | Read-only functions keep working; the refusal is not hidden |
| A12 | Unit missing/failed/masked/start-limit-hit | State and reason available without parsing human-readable status |
| A13 | `status --json`, including all sources failing | One valid JSON, stdout without extra text, a stable schema |
| A14 | `status --check` with ok/degraded/stopped/unknown | Codes 0/1 as agreed |
| A15 | Monochrome, 80×24, narrow terminal, resize, non-TTY | Readability, no panic, correct exit |
| A16 | Slow/hung HTTP, oversized body, redirect | Deadlines/limits respected, no external address contacted |
| A17 | Several ticks during a slow collection | No growing queue of requests/goroutines |
| A18 | Secret/token/URL userinfo/escape in a reply | No leak through JSON/errors and no terminal control executed |
| A19 | Start with stdin=null, SIGTERM, restart limit | P2Pool/XMRig run under the stock units; stop is time-bounded |
| A20 | Independent upstream update | The same native interfaces and wrapper config keep working |
| A21 | Executable path changed | The unit changes, not the wrapper schema; doctor reveals the start error |
| A22 | Both services inactive/disabled | `stopped` differs from a fault; enabled not mixed with active |
| A23 | P2Pool shares=0 / old XMRig rejects | No false alarm or claim of no payouts |
| A24 | Read-only configs and the operator group | The miners start; the operator reads the API but cannot change the configs |
| A25 | Wall clock change, files with a future mtime | Age does not silently go negative/forever fresh; the reason is visible |
| A26 | Stop/restart of P2Pool with the temporary Data API | The old RuntimeDirectory is removed, the new one has the right owner/group/mode |
| A27 | A foreign XMRig API ID or an incompatible uptime | SOURCE_ID_MISMATCH/SOURCE_SESSION_UNKNOWN, health not ok |
| A28 | A drop-in changes systemd dependencies | Doctor checks the effective properties and reports the difference |
| A29 | `node list` with an unreachable/unsynced/slow node | Timeouts respected, ranking excludes the unusable, nothing written |
| A30 | `node select` on a config with comments, without the keys, without rights, `--dry-run` | Only three keys change, the rest byte for byte; permission refusal — code 3; dry-run does not write |

Integration acceptance runs on a separate authorized host/VM with real services and a node/address provided by the user. CPU mining and system changes are never started covertly while preparing the spec. Initial matrix: the current system Debian 13 (systemd 257); a second system with systemd 249 at the owner's discretion after the first pass. Upstream versions, OS, commands and results are recorded in the report.

Non-functional criteria: `status` finishes the bounded collection within 3 seconds plus small overhead; the UI does not block on network reads; the number of goroutines and the ring buffer size are bounded; in 30 minutes of observation there is no monotonic memory growth from accumulating requests/points. Numeric CPU/RSS promises are not made without measurement and are fixed after the prototype.

## 15. Implementation order

This is a sequence of verifiable results, not permission to start implementing or deploy services. The breakdown by packages, files and verification commands is in the [implementation plan](plan.md).

0. **Repository.** Git, module path, license, `go.mod` with pinned versions, directory skeleton, Makefile. Criterion: the empty skeleton builds and passes `vet/fmt/test`; owner decisions D1–D6 from the plan taken.
1. **Contracts and stand.** Confirm the APIs and the service profile on the reference versions, obtain real sanitized fixtures, fix the snapshot/doctor schemas. Criteria: A05–A08, A19, A24; contract errors are fixed before the UI.
2. **Read-only CLI.** Own configuration, adapters, snapshot, health, `status/--json/--check`, `version`, `config path`. Criteria: A03–A08, A12–A14, A16–A18, A22–A23, A25.
3. **Operations.** systemd control, result verification, logs, doctor, `node list/select`, the stock units and the installation guide. Criteria: A01, A09–A12, A19, A21, A24, A29–A30.
4. **TUI.** Presenting the same snapshot, bounded history, control menu, switching to journalctl, terminal cleanup. Criteria: A02, A07–A11, A15, A17–A18.
5. **Release verification.** The full SSH scenario, source outages, independent upstream replacement, compatibility fixtures, documentation. Criteria: A01–A28, especially A20 and A26–A28.

Proposed implementation structure: `cmd/moneroid`, `internal/config`, `internal/status` (model, collector, health), `internal/systemd`, `internal/xmrig`, `internal/p2pool`, `internal/node`, `internal/doctor`, `internal/payouts`, `internal/tui`, `internal/ansi`, `examples`, `systemd`, `testdata`, `docs`. Additional packages appear only with a responsibility of their own.

The detailed task plan with files, interfaces and test commands is the [implementation plan](plan.md). Implementation starts with the CLI and the integration contracts; dashboard styling is not the first stage.

## 16. What counts as readiness of the spec and the product

The spec is ready for implementation after the v1 boundaries are agreed and contradictions with the verified upstream contracts are removed. The product is ready only after the requirements are implemented and the acceptance report exists; documents or a successful compilation do not replace that report.

At the time of writing the handoff and the public documents/sources had been studied. The miners had not been run, real runtime fixtures had not been obtained, the service profile had not been verified on the miners. The first implementation stage closed that technical uncertainty; assumed values must never reach the UI as confirmed.
