# Moneroid v1 source contracts

Verification date: 2026-09-11. Appendix to the [spec](../spec.md).
Public sources were checked; this work involved no runtime runs and no real captures.

## 1. Pinned versions

| Component | Version | Commit | Release date |
|---|---|---|---|
| P2Pool | [v4.17.1](https://github.com/SChernykh/p2pool/releases/tag/v4.17.1) | `9b7395b8c97e7705a138dda2f4edef6b91eebe5f` | 2026-06-28 |
| XMRig | [v6.26.0](https://github.com/xmrig/xmrig/releases/tag/v6.26.0) | `b2ca72480c58d197e18c885d9fc1a0c8d517e60a` | 2026-03-28 |

This is the initially verified compatibility, not a promise to support every version and not a recommendation to pin the miners forever.

Re-check on 2026-09-11: the current P2Pool release is [v4.18](https://github.com/SChernykh/p2pool/releases/tag/v4.18) (2026-08-05); XMRig 6.26.0 is still current. Source line links below refer to 4.17.1; on the stage-1 stand the tables in §2–5 are compared with the actual 4.18 output. The XMRig parameters `watch` (re-reading the config from disk) and `autosave` (writing the config on changes; presence and default confirmed on the stand) are disabled in the shipped example.

## 2. XMRig: GET /2/summary

| Snapshot field in `xmrig` | Upstream | Unit / conversion |
|---|---|---|
| `version` | `version` | string |
| `id` | `id` | string; compared with expected_id |
| `uptime_seconds` | `uptime` | seconds |
| `hashrate_10s_hs` | `hashrate.total[0]` | H/s |
| `hashrate_60s_hs` | `hashrate.total[1]` | H/s |
| `hashrate_15m_hs` | `hashrate.total[2]` | H/s |
| `hugepages_allocated` | `hugepages[0]` | count |
| `hugepages_total` | `hugepages[1]` | count |
| `hugepages_percent` | the previous fields | `100 * allocated / total`, null when total=0 |

Hashrate windows may be null. Main serializers: [Miner.cpp](https://github.com/xmrig/xmrig/blob/b2ca72480c58d197e18c885d9fc1a0c8d517e60a/src/core/Miner.cpp#L145).

| Snapshot field in `xmrig` | Upstream | Value |
|---|---|---|
| `accepted` | `results.shares_good` | accepted submissions |
| `rejected` | `results.shares_total - results.shares_good` | rejected; null plus a field error when total < good |
| `pool` | `connection.pool` | connection address, sanitized before output |
| `connection_uptime_ms` | `connection.uptime_ms` | milliseconds |
| `connected` | `connection.uptime_ms > 0` | derived flag of the current session; unknown field → null |

On disconnect the uptime becomes 0 while the pool name may remain, so a non-empty pool does not prove a connection. The overall work and submission counters say nothing about P2Pool payouts. [NetworkState.cpp: reply](https://github.com/xmrig/xmrig/blob/b2ca72480c58d197e18c885d9fc1a0c8d517e60a/src/base/net/stratum/NetworkState.cpp#L117), [state reset](https://github.com/xmrig/xmrig/blob/b2ca72480c58d197e18c885d9fc1a0c8d517e60a/src/base/net/stratum/NetworkState.cpp#L323).

`msr` exists in the CPU object of `/2/backends`, but v1 does not poll that endpoint. MSR stays unknown and does not affect health. [CpuBackend.cpp](https://github.com/xmrig/xmrig/blob/b2ca72480c58d197e18c885d9fc1a0c8d517e60a/src/backend/cpu/CpuBackend.cpp#L413)

The API must be enabled in the native config; baseline settings: loopback, `restricted=true`. A configured token is sent as Bearer. The application uses no POST/PUT and does not read the config endpoint. [Official API documentation](https://xmrig.com/docs/miner/api), [HTTP settings](https://xmrig.com/docs/miner/config/http)

## 3. P2Pool: periodic local/p2p

| Snapshot field in `p2pool` | Upstream | Unit |
|---|---|---|
| `p2p_connections` | `connections` | count |
| `p2p_incoming_connections` | `incoming_connections` | count |
| `peer_list_size` | `peer_list_size` | number of known peers, not necessarily connected |
| `uptime_seconds` | `uptime` | seconds at write time |
| `zmq_age_at_write_seconds` | `zmq_last_active` | seconds since the last ZMQ activity at write time |
| `zmq_age_seconds` | the previous value + file age | estimate of the current age, only with a sane clock |

Upstream also emits `peers` as an array of strings `dir,ping,?,version,height,ip:port`; v1 takes only the maximum height from them (`peer_max_height`) for `SIDECHAIN_BEHIND`. The file is written nominally once every 60 seconds. Having connections proves neither sidechain consistency, nor full sync, nor the usefulness of every peer. [p2p_server.cpp](https://github.com/SChernykh/p2pool/blob/9b7395b8c97e7705a138dda2f4edef6b91eebe5f/src/p2p_server.cpp#L1773)

## 4. P2Pool: event-driven local/stratum

| Snapshot field in `p2pool` | Upstream | Value |
|---|---|---|
| `hashrate_15m_hs`, `hashrate_1h_hs`, `hashrate_24h_hs` | `hashrate_15m`, `hashrate_1h`, `hashrate_24h` | H/s estimate |
| `stratum_shares` | `total_stratum_shares` | accepted results at the target Stratum difficulty |
| `sidechain_shares_found` | `shares_found` | local shares at sidechain difficulty submitted successfully |
| `sidechain_shares_failed` | `shares_failed` | failed submissions of such shares |
| `average_effort_percent`, `current_effort_percent` | `average_effort`, `current_effort` | percent |
| `last_share_found_at` | `last_share_found_time` | UNIX seconds → RFC3339; 0 → null |
| `stratum_connections` | `connections` | number of connections |

`workers` is an array of strings, not structured worker objects; v1 does not parse it. Stand 2026-09-11 (4.18): the file also contains `wallet` — the payout address in clear text; the field is not read, not printed and replaced in fixtures (SEC-07). There are also `total_hashes` and `block_reward_share_percent`. This data is written on events with rate limiting: no more than once every 20 seconds. There is no guarantee of a new write every 20 seconds. [Serializer and rate limit](https://github.com/SChernykh/p2pool/blob/9b7395b8c97e7705a138dda2f4edef6b91eebe5f/src/stratum_server.cpp#L1574)

## 5. The remaining files

`network/stats`: snapshot `network_height ← height`, `network_difficulty ← difficulty`. `timestamp` is the block time, not the API update time; do not use it for file freshness. The reward field in atomic units is not needed by v1.

`pool/stats`: snapshot `pool_hashrate_hs ← pool_statistics.hashRate`, `sidechain_height ← pool_statistics.sidechainHeight`, `sidechain_difficulty ← pool_statistics.sidechainDifficulty`. `miners` is not an exact count of active miners/local workers; v1 does not show it. The main/mini/nano name is not exported; `sidechain` in the snapshot is null. Both files are updated on events. [p2pool.cpp](https://github.com/SChernykh/p2pool/blob/9b7395b8c97e7705a138dda2f4edef6b91eebe5f/src/p2pool.cpp#L1905)

Stand 2026-09-11 (4.18): `stats_mod`, `local/merge_mining` and `local/console` are created as well; the last one, with stdin not a tty, contains the port and cookie of the P2Pool TCP console — v1 does not read it, and read access to the API directory means console access (see stand report 1, §6).

The files have no extension. The verified implementation writes a temporary file, closes it and renames it to the final name. Only final names are read; consistency of several files as one atomic snapshot is not guaranteed. [p2pool_api.cpp](https://github.com/SChernykh/p2pool/blob/9b7395b8c97e7705a138dda2f4edef6b91eebe5f/src/p2pool_api.cpp#L153)

## 6. Moneroid freshness policy

The following thresholds are product decisions, not upstream norms.

| Source | Rule |
|---|---|
| `systemd_p2pool`, `systemd_xmrig`, `xmrig_summary` | Success of the current collection — ok; after an error a stored value does not confirm health |
| `p2pool_p2p` | mtime no older than 180 s and membership in the current run not refuted; otherwise stale |
| `p2pool_stratum`, `p2pool_network`, `p2pool_pool` | Event data: always show the age; age alone is no reason to declare the source broken |

A ZMQ age > 600 s raises the warning `ZMQ_ACTIVITY_OLD`: no activity was observed for a long time, cause unknown. It is no proof of a ZMQ disconnect or desync. The threshold accounts for the file age.

An mtime more than 5 s in the future → `CLOCK_UNCERTAIN`, age null. On restart the stock systemd RuntimeDirectory removes the previous API files. For a foreign directory without a proven link to the unit the state is unknown. The uptime check with a 5 s tolerance detects a contradiction but is not an exact identification. A wall-clock change/clock incompatibility makes that check unknown; do not refresh the age by re-reading the same file.

A source has a `state`: `ok|stale|unavailable|invalid|permission_denied|unknown`; the times from DATA-06 are stored with it. `not_current_session` is passed as an error_code, not as an extra readiness boolean. Event values are labelled "estimate at write time".

## 7. Structural constraints and reasons

In the snapshot all numeric metrics from the tables are nullable; the boolean `connected` is nullable. v1 needs no MSR, wallet, payout, worker-list or P2Pool version fields: their absence is a feature boundary. `sidechain=null` and `sync_state="unknown"` are set explicitly. The user sees the P2Pool version when checking the binary/journal by hand.

Mandatory reasons: `SOURCE_UNAVAILABLE`, `SOURCE_STALE`, `SOURCE_INVALID`, `FIELD_INVALID`, `PERMISSION_DENIED`, `UNIT_NOT_FOUND`, `UNIT_FAILED`, `UNIT_MASKED`, `UNIT_NOT_ACTIVE`, `UNIT_DISABLED`, `SOURCE_SESSION_UNKNOWN`, `SOURCE_NOT_CURRENT_SESSION` (a proven uptime mismatch), `SOURCE_ID_MISMATCH`, `CLOCK_UNCERTAIN`, `XMRIG_DISCONNECTED`, `HASHRATE_ZERO`, `P2P_NO_CONNECTIONS`, `SIDECHAIN_BEHIND`, `ZMQ_ACTIVITY_OLD`, `NATIVE_CONFIG_NOT_VALIDATED`, `DEPENDENCIES_DIFFER`, `GUARD_HOLD` (spec GUARD-05). Guard reasons (journal and the hold marker only, spec GUARD-02): `SOURCE_STALE`, `P2P_UNTRUSTED`, `P2P_NO_CONNECTIONS`, `ZMQ_ACTIVITY_OLD`, `POOL_STATS_UNKNOWN`, `PEERS_UNKNOWN`, `SIDECHAIN_BEHIND`, `REPAIR`. A missing field is reflected as null; `FIELD_INVALID` is for a wrong type/value, not for every legitimately absent field.

Doctor JSON: `schema_version`, `collected_at`, `checks[]`, `summary`. Each check has `code`, `component`, `result` (`pass|warn|fail|skip`), `message`, `remedy` (string or null). Summary has the counts `pass`, `warn`, `fail`, `skip`.

## 8. Config and service boundaries

P2Pool supports `--params-file` without other CLI parameters; the boolean example is `mini = 1`. `--no-console-log` disables console logging, not the stdin handler; there is no made-up `--no-console`. The console log must not be disabled in an example meant for journald. [COMMAND_LINE.MD](https://github.com/SChernykh/p2pool/blob/9b7395b8c97e7705a138dda2f4edef6b91eebe5f/docs/COMMAND_LINE.MD)

The EOF handler does not stop the pool; the final behaviour with stdin=null and a clean stop are still verified on the stand. [console_commands.cpp](https://github.com/SChernykh/p2pool/blob/9b7395b8c97e7705a138dda2f4edef6b91eebe5f/src/console_commands.cpp#L441)

XMRig writes its console log only when stdout is a tty, pipe or socket (libuv); redirected to a regular file the output is empty (stand 2026-09-11: 0 bytes). Under systemd (`StandardOutput=journal`, a stream socket) the log reaches journald normally; `"syslog": true` is not needed and duplicates lines. XMRig allows comments and trailing commas in its native JSON. Validating its config with strict `encoding/json` would give false rejections, so Moneroid does not do it. [Json_unix.cpp](https://github.com/xmrig/xmrig/blob/b2ca72480c58d197e18c885d9fc1a0c8d517e60a/src/base/io/json/Json_unix.cpp#L29)
