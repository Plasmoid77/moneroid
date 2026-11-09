# Changelog

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
