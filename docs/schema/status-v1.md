# Schema of `moneroid status --json`, schema_version 1

Generated from a live snapshot on 2026-09-11. All numeric fields and `connected` are nullable: `null` means "unknown", `0` a measured zero. Time is RFC3339 UTC. `sources.*` are seven sources: `systemd_p2pool`, `systemd_xmrig`, `xmrig_summary`, `p2pool_p2p`, `p2pool_stratum`, `p2pool_network`, `p2pool_pool`; `services.*` are `p2pool`, `xmrig`. `health.level`: `ok|degraded|stopped|starting|unknown`; `sources.*.state`: `ok|stale|unavailable|invalid|permission_denied|unknown`. Units and upstream origin are in the [contracts](../research/upstream-contracts.md). Within a version fields are only added (`p2pool.peer_max_height`, `sources.*.error_code` and `sources.*.message` were added after the snapshot).

| Field | Type in the example |
|---|---|
| `schema_version` | integer |
| `collected_at` | string |
| `collection_duration_ms` | integer |
| `services.*.unit` | string |
| `services.*.load_state` | string |
| `services.*.active_state` | string |
| `services.*.sub_state` | string |
| `services.*.enabled_state` | string |
| `services.*.pid` | integer |
| `services.*.invocation_id` | string |
| `services.*.uptime_seconds` | number |
| `services.*.restart_count` | integer |
| `services.*.last_exit_status` | integer |
| `services.*.result` | string |
| `sources.*.state` | string |
| `sources.*.observed_at` | string |
| `sources.*.last_success_at` | string |
| `sources.*.source_updated_at` | string |
| `sources.*.age_seconds` | number |
| `xmrig.version` | string |
| `xmrig.id` | string |
| `xmrig.uptime_seconds` | integer |
| `xmrig.hashrate_10s_hs` | number |
| `xmrig.hashrate_60s_hs` | number |
| `xmrig.hashrate_15m_hs` | null |
| `xmrig.hugepages_allocated` | integer |
| `xmrig.hugepages_total` | integer |
| `xmrig.hugepages_percent` | integer |
| `xmrig.accepted` | integer |
| `xmrig.rejected` | integer |
| `xmrig.pool` | string |
| `xmrig.connection_uptime_ms` | integer |
| `xmrig.connected` | boolean |
| `p2pool.p2p_connections` | integer |
| `p2pool.p2p_incoming_connections` | integer |
| `p2pool.peer_list_size` | integer |
| `p2pool.uptime_seconds` | integer |
| `p2pool.zmq_age_at_write_seconds` | integer |
| `p2pool.zmq_age_seconds` | number |
| `p2pool.hashrate_15m_hs` | integer |
| `p2pool.hashrate_1h_hs` | integer |
| `p2pool.hashrate_24h_hs` | integer |
| `p2pool.stratum_shares` | integer |
| `p2pool.sidechain_shares_found` | integer |
| `p2pool.sidechain_shares_failed` | integer |
| `p2pool.average_effort_percent` | integer |
| `p2pool.current_effort_percent` | integer |
| `p2pool.last_share_found_at` | null |
| `p2pool.stratum_connections` | integer |
| `p2pool.network_height` | integer |
| `p2pool.network_difficulty` | integer |
| `p2pool.network_reward_atomic` | integer, atomic units (since v0.5.2) |
| `p2pool.pool_hashrate_hs` | integer |
| `p2pool.sidechain_height` | integer |
| `p2pool.sidechain_difficulty` | integer |
| `p2pool.sidechain` | null |
| `p2pool.sync_state` | string |
| `health.level` | string |
| `health.issues[]` | array |
| `health.issues[].code` | string |
| `health.issues[].severity` | string |
| `health.issues[].component` | string |
| `health.issues[].message` | string |
