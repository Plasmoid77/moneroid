# Troubleshooting

The first step is always `moneroid doctor`, the second `moneroid logs <target>`. Moneroid shows consequences and reason codes but does not guess the cause on upstream's behalf (spec DOC-04).

| Symptom / code | Meaning | What to do |
|---|---|---|
| `UNIT_NOT_FOUND` | Unit not installed or no `daemon-reload` | Install the unit file, `systemctl daemon-reload` |
| `UNIT_FAILED`, `Result=start-limit-hit` | The service crashed 5 times within a minute | `moneroid logs`; usually a wrong `wallet` or an unreachable node; after fixing, no `systemctl reset-failed` is needed — `moneroid start` |
| `UNIT_MASKED` | Unit is masked | `systemctl unmask` |
| `UNIT_NOT_ACTIVE` (health degraded) | Only one service is running | `moneroid start <the other>` |
| `SOURCE_UNAVAILABLE` for `xmrig_summary`, service active | HTTP API does not answer | In `xmrig.json`: `http.enabled=true`, `host=127.0.0.1`, port as in `api_url` |
| `PERMISSION_DENIED` for `xmrig_summary` | 401/403 | The token in `token_file` differs from `http.access-token`; `restart xmrig` after changing it |
| `SOURCE_ID_MISMATCH` | A different XMRig on the port | `api.id` in `xmrig.json` = `expected_id` in `moneroid.toml` |
| `PERMISSION_DENIED` for `p2pool_*` | The operator is not in group `moneroid` or has not logged in again | `usermod -aG moneroid`, new login (the message names the missing group) |
| `SOURCE_UNAVAILABLE` for `p2pool_p2p`, service active | The file is not written yet (first write after ~60 s) or `data-api`/`local-api` are not set | Wait a minute; check `p2pool.conf` |
| `SOURCE_STALE` (`local/p2p` older than 180 s) | P2Pool hung or stopped writing the API | `moneroid logs p2pool`, `restart p2pool` |
| `SOURCE_NOT_CURRENT_SESSION` | Files/API belong to another process | Make sure there is no second P2Pool/XMRig; for foreign units — `RuntimeDirectory` |
| `ZMQ_ACTIVITY_OLD` | >600 s without ZMQ events | Node without `--zmq-pub`, wrong `zmq-port` or an unstable ZMQ on a remote node (repeated `ZMQReader disconnected` in the journal) → `moneroid node list`, pick another |
| `SIDECHAIN_BEHIND` | Local sidechain below the peers' height | Normal for the first ~10 minutes after start (P2Pool downloads and verifies the window; `verified block` lines grow in the journal); if it never catches up — `moneroid logs p2pool`, node/network |
| `P2P_NO_CONNECTIONS` | No peers | Outgoing connections to the sidechain port (mini 37888, main 37889, nano 37890) are blocked |
| `HASHRATE_ZERO`, `XMRIG_DISCONNECTED` | XMRig gets no jobs | P2Pool is still syncing (`SideChain SYNCHRONIZED` in the journal) or `pools[0].url` ≠ P2Pool's `stratum` |
| `CLOCK_UNCERTAIN` | File mtime in the future | Clock/NTP |
| `FIELD_INVALID` | Upstream changed a field type | The metric is null, everything else works; report an issue with the upstream version |
| XMRig: `FAILED TO APPLY MSR MOD`, hugepages 0% | Expected under an unprivileged user | See install.md §6 |
| hugepages reserved but XMRig < 100% | P2Pool took the pages (its own RandomX dataset) or fewer than 1040 are free | `light-mode = 1` in `p2pool.conf`, `nr_hugepages ≥ 1536`, reboot |
| `moneroid start`: exit 3 | No permission on systemd | `sudo` or the polkit rule |
| `moneroid start`: exit 4 | systemctl did not answer within 90 s | The job may have continued: `moneroid status` |
| `tui`: "needs an interactive terminal" | No TTY | Use `status` |

What is **not** an error: `sidechain shares 0 found` (a share on mini is found rarely), old `rejected` that do not grow, `UNIT_DISABLED` (autostart simply not enabled), `hashrate_15m = null` for the first 15 minutes.

## The uplink filters traffic to nodes

Symptom (Zeonux, 2026-09-14): `get_info` works, but replies larger than ~15 KB (`get_block_headers_range`, sidechain sync) hang towards every node regardless of port and TLS; `node list` shows `headers: context deadline exceeded`, P2Pool loops in "Couldn't download block headers" or mines a chain of its own (`SIDECHAIN_BEHIND`).

P2Pool can go through SOCKS5 (P2P, RPC and ZMQ): in `p2pool.conf`

```
socks5 = 127.0.0.1:1080
socks5-proxy-type = plain
```

The proxy source is any host with clean internet, e.g. `ssh -N -D 127.0.0.1:1080 user@host` (restrict the key on that side with `restrict,port-forwarding`), or a VPN client on the host itself. With `socks5` set, `moneroid node list/select` and `doctor` probe the nodes through the same proxy (loopback/LAN addresses go direct, as in P2Pool). After the network is fixed, delete the cache of the isolated chain: `moneroid stop p2pool; rm /var/lib/moneroid/p2pool/p2pool.cache /var/lib/moneroid/p2pool/p2pool_peers.txt; moneroid start p2pool` — `moneroid-guard.service` does this by itself (spec GUARD-06).

Such uplinks may also filter GitHub release downloads, distribution mirrors and the I2P transports; see `install.md` for the download cache and the i2pd reseed proxy.
