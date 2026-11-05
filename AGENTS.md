# Maintainer guide (for people and AI agents)

This file is the contract for any change in the repository. Read it in full before editing.

## What this is and what it must not become

Moneroid is a thin layer over two foreign programs (P2Pool, XMRig) and systemd. The owner's goal: **write it once and not maintain it**. Hence the main criterion for any change: *can a stock systemd / journalctl / upstream command do this?* If yes, the change is not needed.

Do not add: daemons (the one exception is `moneroid guard`, owner decision 2026-11-05, spec §7.1), databases, a web UI, auto-updates, binary downloads inside the tool, `monerod` management, multiple stacks, wallet balances and external pool observers, log parsing as a metrics source (exception: payout events, `internal/payouts`), forks/vendoring of upstream, new dependencies without a concrete need.

## Where things are

| Question | File |
|---|---|
| Requirements, boundaries, reason codes, acceptance scenarios A01–A30 | `docs/spec.md` |
| How the code is built, invariants | `docs/architecture.md` |
| Where each field comes from, units, freshness | `docs/research/upstream-contracts.md` |
| What was actually verified and where | `docs/research/acceptance-report-v1.md`, `stand-report-*.md` |
| Install / troubleshoot / update for the user | `docs/install.md`, `docs/troubleshooting.md`, `docs/updating.md` |
| JSON schemas | `docs/schema/` |
| Decision history | `docs/plan.md` (§0, §10), `docs/history/handoff-v2.md` |

## Rules for code changes

1. Spec first, then code: new behaviour first gets a requirement with a code (e.g. `NODE-06`) in `docs/spec.md`, then the implementation, then a line in `docs/research/acceptance-report-v1.md` if it was verified live.
2. Every reason code (`SOURCE_STALE`, `SIDECHAIN_BEHIND`, …) is a stable contract for automation: do not rename, do not change the meaning. Add new codes to the list in `docs/research/upstream-contracts.md` §7.
3. The JSON `schema_version` changes only when a field is removed or renamed. Adding a field keeps the version.
4. All numeric metrics are nullable; `0` only for a measured zero.
5. No secrets in output, JSON, errors or fixtures: payout address, tokens, the P2Pool console cookie. `sanitize()` for strings from upstream.
6. Tests: `make check` is mandatory and must pass without network and without systemctl. New upstream fields come through fixtures in `testdata/` (real captures with addresses replaced; the upstream version in the directory name).
7. One commit — one change with a clear message. Do not push to `main` without `make check`.

## Live verification

Unit tests are not enough for the systemd part. Minimal stand: any Linux system with systemd ≥ 249, P2Pool and XMRig from the official releases, a remote Monero node with RPC+ZMQ. `install.sh` sets the stand up and `install.sh --uninstall --purge` removes it completely. A clean VM (libvirt + cloud image) is the cheapest way to verify an "install from scratch by the instructions" run.

Do not start mining on other people's or production machines without the owner's explicit permission; after testing the services — `moneroid stop`.

## Known traps (from experience)

- After start P2Pool logs `SideChain SYNCHRONIZED` on its own empty chain and only minutes later switches to the real one; watch `SIDECHAIN_BEHIND`/`SIDECHAIN_SYNC`.
- P2Pool takes the first DNS answer (possibly an unreachable IPv6) — `node select` writes an IP literal.
- An open TCP port 18083 ≠ ZMQ; the probe does a ZMTP handshake.
- Filtering uplinks cut replies > ~15 KB while `get_info` still works; P2Pool supports `socks5`, and Moneroid probes nodes through the same proxy (private addresses go direct, as in P2Pool).
- Under an unprivileged user XMRig applies no MSR mod and allocates no hugepages itself — see `docs/install.md` §6.
- `RuntimeDirectory` is removed on stop — that is the proof that the Data API files belong to the current process.
- Switching sidechains (mini/nano/main) needs `p2pool_peers.txt` and `p2pool.cache` removed, otherwise P2Pool bans the old peers one by one and "synchronizes" an empty chain of its own.
- Debian: `/usr/sbin/nologin`; Ubuntu 22.04: polkit 0.105 without `rules.d`; the Linux console (`TERM=linux`) has no alternate screen.
