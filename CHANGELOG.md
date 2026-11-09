# Changelog

## v0.5.10 — 2026-11-10

- TUI: the rate sources on the `rates` line are divided by an orange `│` (`|` on a console font without it).

## v0.5.9 — 2026-11-10

- TUI: the XMR rate from three sources at once — CoinGecko, Kraken (the official Monero GUI wallet's default) and CoinPaprika, asked in parallel. A new `rates` line shows each one (stale with the reason when it fails); the `EARNINGS` rate and all USD/EUR amounts use the median of those that answered (spec PRICE-01, UI-07). On a narrow terminal the sources that do not fit move to the next line instead of being cut off (found by an independent Codex review).
- TUI: the `PEERS` table from v0.5.6 is gone. P2Pool chooses its peers itself, at random by design, so there is nothing to act on; the `node` ping line stays.
- TUI: P2Pool's version next to `P2POOL`, as XMRig's already is — read from the unit's binary (`p2pool --version`) once per P2Pool run (spec UI-09).

## v0.5.8 — 2026-11-10

- TUI: a second source for the XMR rate. When CoinGecko fails (timeout, `HTTP 429` — common behind a shared VPN exit), CoinPaprika is asked; it is one of the two providers of the official Monero GUI wallet and not an exchange. A rate that its provider last updated more than an hour ago is refused (found by an independent Codex review), so a stalled source cannot pass for fresh. The rate line names the source it came from; with both down, both reasons are shown. Each request has a 10 s timeout (spec PRICE-01, PRICE-02).

## v0.5.7 — 2026-11-10

- TUI: the XMR rate (and with it every USD/EUR amount) could stay "price unavailable" for 10 minutes. The CoinGecko request used the node probes' 3 s timeout, and through a VPN on a mobile uplink CoinGecko often takes 3–5 s; one slow first answer hid the rate until the next request. It now has its own 20 s timeout and is retried after 1 minute when it fails (10 minutes after a good answer, as before). The error reads as one line ("CoinGecko: no answer in time") instead of Go's message with the URL (spec PRICE-01, PRICE-02).

## v0.5.6 — 2026-11-09

- TUI: pings, as in Gupax (spec UI-08). A `node` line under `P2POOL` pings the Monero node from P2Pool's config every minute (one `get_info`, through P2Pool's `socks5` if set): latency with the median of the last 5 (single slow answers over a VPN are common), sync state, height. A `PEERS` block lists the connected P2Pool peers sorted by ping — address, ping, connection age, direction, software — with min/median/max; it takes only the rows the screen has left, so warnings and issues stay visible.

## v0.5.5 — 2026-11-09

- TUI: the 10-minute sparkline is scaled to its own range (at least the top 10 %), so a steady hashrate shows its variation instead of a flat bar at the top; the label gives the scale (`13055–14506 H/s`). On a console font without eighth blocks it is a thin trace of `_ ─ ¯` rather than solid shades, which merged with the full hugepages meter above; a blank line separates the two when the terminal is at least 30 rows high.

## v0.5.4 — 2026-11-09

- Linux console: instead of a fixed CP437 list, the TUI and `status` ask the kernel which characters the loaded console font has (`GIO_UNIMAP`) and replace only the missing ones. With console-setup installed (Debian's default Lat15 font) `€`, `—` and `…` show as they are; v0.5.3 also put `▓` into the sparkline, which Lat15 lacks.
- Durations are written with a space: `3m 05s`, `12h 07m` (TUI, `status`); the TUI uptime column is one cell wider.
- `install.sh` (found by an independent Codex review): an argument with a newline (`--node`, `--wallet`, versions) passed validation on its first good line and could carry a `sed` command run as root — now each value must match as a whole. A custom `MONEROID_CACHE` is used only if every parent directory is root-owned and closed to others too (a user owning a parent could swap the cache after the check). The `--i2p` node check and the i2pd install now run before any file is replaced; if the script still stops after replacing binaries and units, it reloads systemd and says that the services were not restarted.

## v0.5.3 — 2026-11-09

- TUI and `status` on the Linux console (`TERM=linux`, e.g. the server's own monitor): the kernel's built-in font has only CP437, so `€`, `—`, `…` and the sparkline's `▁▂▃▅▆▇` showed as squares. There they are now written as `EUR`, `-`, `...` and a `░▒▓█` sparkline (spec UI-06).

## v0.5.2 — 2026-11-09

- TUI: an `EARNINGS` block — the XMR rate in USD and EUR (CoinGecko, every 10 min, through P2Pool's `socks5` if set), payouts with their value at today's rate, and the expected XMR a day/a month for the current hashrate; the payouts screen gets a "≈ at today's rate" column. `[ui] price = false` disables the request (spec UI-07, PRICE-01..03).
- `status --json`: `p2pool.network_reward_atomic` (the block reward from `network/stats`).
- The orange is now exactly the Monero logo's `#FF6600` (was `#FF8000`).
- Go 1.27.3 (go.mod), so CI and local builds use the same toolchain and `make dist` is byte-identical.

## v0.5.1 — 2026-11-09

- `install.sh` installs the newest stable releases of P2Pool, XMRig and Moneroid instead of versions pinned in the script; re-running it updates everything. P2Pool/XMRig are accepted only when their checksum lists carry a valid signature of the authors' keys pinned by fingerprint (public keys in `keys/`, spec INSTALL-06); `--p2pool-version`, `--xmrig-version`, `--moneroid-version` install given versions. gpg is installed if missing. Without an explicit version it never downgrades below the installed binary, refuses revoked/expired signing keys and uses the download cache only when it is root-owned and not writable by others.
- `install.sh` adds the operator to `systemd-journal` as well (logs, payouts and the TUI payouts line without sudo).
- Releases are built and published by CI from a pushed tag (`make dist`).
- `payouts` and the TUI payouts line: a user outside `systemd-journal`/`adm` got "no payouts" instead of an access error, because `journalctl -q` hid the refusal (PAY-03). Now `-q` is gone and the error says to add the user to `systemd-journal`.

## v0.5.0 — 2026-11-05

- `moneroid guard` + `moneroid-guard.service` (spec §7.1): holds XMRig while P2Pool is not ready (behind its peers, no peers, silent node, stale or untrusted Data API) with 3/3 hysteresis, and repairs an isolated sidechain by moving `p2pool.cache`/`p2pool_peers.txt` aside — at most once per incident and once per hour. Tunable in an optional `[guard]` section.
- `status`/`tui`/`doctor`: `GUARD_HOLD` replaces `UNIT_NOT_ACTIVE` while the guard holds XMRig, and the P2Pool issues behind the hold stay visible; JSON field `guard`.
- `moneroid start|stop|restart` and the TUI cancel the guard's hold when they act on XMRig.
- `install.sh` installs the guard unit, enables/starts it with `--enable`, restarts it on upgrade and stops it first on `--uninstall`.
- The whole repository is in English (docs, examples, comments); the obsolete stand scripts are replaced by `install.sh` / `install.sh --uninstall`.

## v0.4.3 — 2026-09-17

- TUI: the screen is erased explicitly on exit (like htop), so terminals without an alternate screen (Linux console, SOL, `screen` without `altscreen`) no longer keep the last frame.
- `status`/`tui`: a `PERMISSION_DENIED` on the Data API says which group the process lacks (a new login is needed after `usermod`).

## v0.4.2 — 2026-09-17

- `install.sh --i2p`: respects an already installed i2pd (`tunnelsdir`, console and SOCKS endpoints from `i2pd.conf`); `node select` runs only for a freshly seeded config (an existing `host = 127.0.0.1` is a local node and is left alone).
- `docs/install.md`: remote node + I2P through a local port forward; i2pd reseed through a proxy on a filtering uplink.
- `install.sh`: cache of verified downloads `/var/cache/moneroid` (`MONEROID_CACHE`) — installation without GitHub access; waits up to 120 s for the i2pd tunnel.

## v0.4.1 — 2026-09-16

- `install.sh --i2p`: P2Pool p2p over I2P (i2pd, server tunnel, `.b32.i2p` in `p2pool.conf`); needs a node on LAN/localhost or a `.b32.i2p` one.
- `node`/`doctor`: with `socks5` in `p2pool.conf`, loopback/LAN node addresses are probed directly, as P2Pool itself does (needed for Tor/I2P with a local node).
- `docs/install.md`: switching sidechains (cache cleanup), main/mini/nano parameters from the sources.
- `install.sh`, after the Codex review: strict `--wallet`/`--node` validation (base58, IPv6 `[addr]:RPC:ZMQ`), `restart` on re-run, i2pd tunnel by name and by the chain from the config, `--now` for the MSR unit, `--uninstall` stops each service separately and removes the tunnel.

## v0.4.0 — 2026-09-16

- **Project renamed to Moneroid.** Binary, Go module, units `moneroid-p2pool.service`/`moneroid-xmrig.service`, group and users `moneroid*`, directories `/etc/moneroid`, `/var/lib/moneroid`, `/run/moneroid-p2pool-api`, `api.id = moneroid-xmrig`. Migration from Monerizer — `docs/install.md`. Entries below about earlier versions read with the old name in mind.
- `install.sh` — one-command installation as a user with sudo: official P2Pool 4.18 and XMRig 6.26.0 releases with pinned SHA256, wallet/sidechain/node as arguments, `--enable`, `--hugepages` (sysctl + MSR unit on Intel), `--uninstall [--purge]`; idempotent.

## v0.3.0 — 2026-09-16

- TUI: a full-height screen in the spirit of `htop` — title and key bars, hashrate/hugepages/sidechain-sync meters, pool share estimate, payouts line (re-read once a minute).
- Monero colours (orange/white, red for problems) in `tui` and in the text `status`; in a pipe and with `NO_COLOR` the output stays plain text; `status` time in the local zone.
- `github.com/charmbracelet/colorprofile` became a direct dependency (it was already in the graph via Bubble Tea).

## v0.2.0 — 2026-09-16

- `payouts [--json] [--since]` — payouts from the P2Pool journal (the "got a payout of" / "didn't get a payout" lines), total and the number of blocks without a share (PAY-01..03, schema `payouts-v1`).
- TUI: payouts screen on `p` (last 12, total, `r` reloads).
- Time in the dashboard and in `payouts` is the system's local zone; JSON stays RFC 3339 UTC.
- `docs/troubleshooting.md`: section on filtering uplinks and `socks5` in `p2pool.conf`.

## v0.1.0 — 2026-09-16

First release. `status [--json] [--check]`, `tui`, `start|stop|restart` (with automatic `reset-failed`), `logs`, `doctor` (31 checks), `node list|select` (probe: `get_info` + block headers + ZMTP, through SOCKS5 when `socks5` is set in `p2pool.conf`), `config path`, `version`. Shipped: systemd unit files, example configs, polkit rule, MSR unit (Intel), documentation. Verified on Arch (systemd 261), Debian 13 (257), Ubuntu 22.04 (249) with P2Pool 4.18 and XMRig 6.26.0.
