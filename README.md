# Moneroid

*Minimal CLI/TUI to run one P2Pool + XMRig pair under systemd on Linux: status, health, doctor, logs, start/stop, payouts, node probing. One static binary; the upstream programs stay untouched.*

A small CLI/TUI for operating one **P2Pool + XMRig** pair under systemd on Linux. A single binary without host dependencies; the miners remain the stock upstream programs with their native configs and are updated independently. Moneroid mines nothing, stores nothing and rewrites nothing — it only uses `systemctl`, `journalctl`, the XMRig HTTP API and the P2Pool Data API files.

```text
XMRig ──Stratum──▶ P2Pool ──RPC/ZMQ──▶ Monero node
   ▲                  ▲
   └── moneroid ─────┘   (systemd · GET /2/summary · reads /run/…-api)
```

Status: v1 is implemented and verified on Arch Linux (systemd 261), Debian 13 (systemd 257) and Ubuntu 22.04 (systemd 249) with P2Pool 4.18 and XMRig 6.26.0 — [acceptance report](docs/research/acceptance-report-v1.md).

## What it does

| Command | What it does |
|---|---|
| `moneroid status [--json] [--check]` | Service states, XMRig/P2Pool metrics, source freshness, health with reasons. `--check` → exit 1 unless health is `ok` |
| `moneroid tui` | Live full-screen dashboard in the spirit of `htop`: hashrate with meters and a sparkline, P2Pool, earnings (XMR rate in USD/EUR from CoinGecko, CoinPaprika as the fallback, payouts and the expected XMR a day/month in XMR, $ and €; `[ui] price = false` turns the rate off), issues; start/stop/restart menu with confirmation, journal view, payouts screen (`p`) |
| `moneroid start\|stop\|restart [all\|p2pool\|xmrig]` | Control through systemd; prints the actual state afterwards |
| `moneroid logs [--follow] [--lines N] [target]` | `journalctl` by exact unit names |
| `moneroid doctor [--json]` | 31 checks: units, dependencies, permissions, APIs, freshness, node |
| `moneroid payouts [--json] [--since TIME]` | Payouts from the P2Pool journal: time, amount, block, total; the wallet address is never printed |
| `moneroid node list` / `node select [--dry-run]` | Probes the nodes from `nodes.txt` (RPC latency, sync, ZMQ port); `select` rewrites `host/rpc-port/zmq-port` in `p2pool.conf` |
| `moneroid guard` | Run by the optional `moneroid-guard.service`: keeps XMRig stopped while P2Pool is not synced with its sidechain (or has no peers/node), starts it again when P2Pool is ready, and resets an isolated sidechain cache automatically ([spec §7.1](docs/spec.md)). Log: `journalctl -u moneroid-guard` |
| `moneroid config path`, `version` | Utilities |

`status` and `tui` use the Monero colours (orange/white; red only for problems); in a pipe or with `NO_COLOR=1` the output is plain text, and the meaning is always in the text.

Boundaries (deliberate): no daemon except the optional guard, no binary installer inside the tool, no auto-updates, no database, no web UI, no `monerod` management, no multiple stacks, no kernel/MSR/hugepages tuning inside the tool. The full list is in the [spec](docs/spec.md).

## Requirements

- Linux x86_64, systemd ≥ 249 (verified up to 261); polkit only for the optional password-less control.
- Installed [P2Pool](https://github.com/SChernykh/p2pool/releases) and [XMRig](https://github.com/xmrig/xmrig/releases) binaries (official releases, checksum/signature verified).
- A Monero node with RPC and ZMQ (`--zmq-pub`) open: your own or a remote one. A remote node sees the host's IP and the payout address.
- A primary Monero wallet address (starts with `4`).

## Installation

One command (Linux x86_64 with systemd; Debian 13, Ubuntu 22.04 and Arch verified), as a user with sudo:

```sh
curl -fsSLO https://raw.githubusercontent.com/Plasmoid77/moneroid/main/install.sh
sh install.sh --wallet 4YOUR_PRIMARY_ADDRESS          # + --enable (autostart), --hugepages, --node HOST:RPC:ZMQ, --sidechain mini|nano|main
```

`--i2p` (together with `--node LAN_IP:RPC:ZMQ` or a `.b32.i2p` node) installs `i2pd`, creates a server tunnel and moves P2Pool's p2p traffic into I2P — see the section in `docs/install.md`.

The script downloads the newest official P2Pool and XMRig releases and the newest Moneroid release, accepts P2Pool/XMRig only if their checksum lists carry a valid signature of the authors' keys pinned in the script (fingerprints; the public keys are in `keys/`), checks Moneroid against its release `SHA256SUMS`, creates the group and two system users, adds you to the groups `moneroid` and `systemd-journal` (status, logs and payouts without sudo; log in again), installs the configs and unit files, picks a Monero node by probing `nodes.txt`, starts the services and shows `doctor`. Running it again overwrites nothing in `/etc/moneroid`. Running it again updates everything to the newest releases; `--p2pool-version v4.18 --xmrig-version 6.26.0 --moneroid-version v0.5.1` installs given versions (rollback, or offline from a pre-filled `/var/cache/moneroid`). Removal: `sh install.sh --uninstall [--purge]`.

Manually, step by step (or from source: `make build`, Go 1.27) — [docs/install.md](docs/install.md).

## Permissions

- `status`, `tui`, `doctor`, `logs` — a regular user in group `moneroid` (reads the Data API and configs) plus `systemd-journal`/`wheel` for the journal.
- `start/stop/restart` — through `sudo` or the optional polkit rule `examples/polkit/50-moneroid.rules` (two units only, start/stop/restart only, group `moneroid` only).
- `node select` writes `p2pool.conf` → `sudo`.
- `moneroid-guard.service` runs as root with a single capability (`CAP_DAC_OVERRIDE`) and a read-only filesystem except the P2Pool state directory; `moneroid start|stop xmrig` cancels its hold (through `sudo` when the operator is not in group `moneroid`).
- The Data API directory contains `local/console` with the cookie of the P2Pool TCP console: read access to the directory equals control of P2Pool, hence a single group.

## How it works and how to maintain it

- [docs/architecture.md](docs/architecture.md) — data flow, packages, invariants, health rules.
- [AGENTS.md](AGENTS.md) — change rules for people and AI agents, known traps.
- [CHANGELOG.md](CHANGELOG.md) · releases on GitHub, `sha256sum -c SHA256SUMS`.
- `make check` — gofmt, vet, tests (no network, no systemctl), static build; CI does the same.

## Documents

- [Installation](docs/install.md) · [Troubleshooting](docs/troubleshooting.md) · [Updating upstream](docs/updating.md)
- [Spec v1](docs/spec.md) · [Implementation plan and decision history](docs/plan.md)
- [Source contracts](docs/research/upstream-contracts.md) · [Stand report 1](docs/research/stand-report-1.md) · [2](docs/research/stand-report-2.md) · [Acceptance v1](docs/research/acceptance-report-v1.md)
- [Original handoff](docs/history/handoff-v2.md) — the initial research; the spec's decisions take precedence.

License — MIT. XMRig and P2Pool are not part of the distribution and come under their own licenses.
