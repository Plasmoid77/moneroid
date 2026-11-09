// Package status defines the MiningSnapshot (spec §10), collects it (§9) and
// evaluates health (§11).
package status

import "time"

// Source states (contracts §6).
const (
	StateOK               = "ok"
	StateStale            = "stale"
	StateUnavailable      = "unavailable"
	StateInvalid          = "invalid"
	StatePermissionDenied = "permission_denied"
	StateUnknown          = "unknown"
)

// Health levels (H-02).
const (
	LevelOK       = "ok"
	LevelDegraded = "degraded"
	LevelStopped  = "stopped"
	LevelStarting = "starting"
	LevelUnknown  = "unknown"
)

// Source names used as keys of Snapshot.Sources.
const (
	SrcSystemdP2Pool = "systemd_p2pool"
	SrcSystemdXMRig  = "systemd_xmrig"
	SrcXMRigSummary  = "xmrig_summary"
	SrcP2PoolP2P     = "p2pool_p2p"
	SrcP2PoolStratum = "p2pool_stratum"
	SrcP2PoolNetwork = "p2pool_network"
	SrcP2PoolPool    = "p2pool_pool"
)

type Snapshot struct {
	SchemaVersion        int               `json:"schema_version"`
	CollectedAt          time.Time         `json:"collected_at"`
	CollectionDurationMs int64             `json:"collection_duration_ms"`
	Services             Services          `json:"services"`
	Sources              map[string]Source `json:"sources"`
	XMRig                XMRigMetrics      `json:"xmrig"`
	P2Pool               P2PoolMetrics     `json:"p2pool"`
	Health               Health            `json:"health"`
	// Guard is present only while `moneroid guard` holds XMRig (GUARD-05).
	Guard *GuardState `json:"guard,omitempty"`
	// Props keeps the raw systemctl properties for doctor; not serialized.
	Props map[string]map[string]string `json:"-"`
}

type Services struct {
	P2Pool Service `json:"p2pool"`
	XMRig  Service `json:"xmrig"`
}

type Service struct {
	Unit           string   `json:"unit"`
	LoadState      string   `json:"load_state"`
	ActiveState    string   `json:"active_state"`
	SubState       string   `json:"sub_state"`
	EnabledState   string   `json:"enabled_state"`
	PID            *int64   `json:"pid"`
	InvocationID   string   `json:"invocation_id"`
	UptimeSeconds  *float64 `json:"uptime_seconds"`
	RestartCount   *int64   `json:"restart_count"`
	LastExitStatus *int64   `json:"last_exit_status"`
	Result         string   `json:"result"`
	// runtimeDir/runtimePreserve mirror RuntimeDirectory[Preserve]; internal.
	runtimeDir      string
	runtimePreserve string
}

// Source describes one data source per DATA-06.
type Source struct {
	State           string     `json:"state"`
	ErrorCode       string     `json:"error_code,omitempty"`
	Message         string     `json:"message,omitempty"`
	ObservedAt      *time.Time `json:"observed_at"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	SourceUpdatedAt *time.Time `json:"source_updated_at"`
	AgeSeconds      *float64   `json:"age_seconds"`
}

type XMRigMetrics struct {
	Version            string   `json:"version"`
	ID                 string   `json:"id"`
	UptimeSeconds      *int64   `json:"uptime_seconds"`
	Hashrate10s        *float64 `json:"hashrate_10s_hs"`
	Hashrate60s        *float64 `json:"hashrate_60s_hs"`
	Hashrate15m        *float64 `json:"hashrate_15m_hs"`
	HugepagesAllocated *int64   `json:"hugepages_allocated"`
	HugepagesTotal     *int64   `json:"hugepages_total"`
	HugepagesPercent   *float64 `json:"hugepages_percent"`
	Accepted           *int64   `json:"accepted"`
	Rejected           *int64   `json:"rejected"`
	Pool               string   `json:"pool"`
	ConnectionUptimeMs *int64   `json:"connection_uptime_ms"`
	Connected          *bool    `json:"connected"`
}

type P2PoolMetrics struct {
	P2PConnections         *int64     `json:"p2p_connections"`
	P2PIncomingConnections *int64     `json:"p2p_incoming_connections"`
	PeerListSize           *int64     `json:"peer_list_size"`
	PeerMaxHeight          *int64     `json:"peer_max_height"` // highest sidechain height reported by connected peers
	PeerHeights            []int64    `json:"-"`               // every reported peer height, for the guard
	UptimeSeconds          *int64     `json:"uptime_seconds"`
	ZMQAgeAtWriteSeconds   *int64     `json:"zmq_age_at_write_seconds"`
	ZMQAgeSeconds          *float64   `json:"zmq_age_seconds"`
	Hashrate15m            *float64   `json:"hashrate_15m_hs"`
	Hashrate1h             *float64   `json:"hashrate_1h_hs"`
	Hashrate24h            *float64   `json:"hashrate_24h_hs"`
	StratumShares          *int64     `json:"stratum_shares"`
	SidechainSharesFound   *int64     `json:"sidechain_shares_found"`
	SidechainSharesFailed  *int64     `json:"sidechain_shares_failed"`
	AverageEffortPercent   *float64   `json:"average_effort_percent"`
	CurrentEffortPercent   *float64   `json:"current_effort_percent"`
	LastShareFoundAt       *time.Time `json:"last_share_found_at"`
	StratumConnections     *int64     `json:"stratum_connections"`
	NetworkHeight          *int64     `json:"network_height"`
	NetworkDifficulty      *float64   `json:"network_difficulty"`
	NetworkReward          *int64     `json:"network_reward_atomic"` // block reward of the current Monero block, atomic units
	PoolHashrate           *float64   `json:"pool_hashrate_hs"`
	SidechainHeight        *int64     `json:"sidechain_height"`
	SidechainDifficulty    *float64   `json:"sidechain_difficulty"`
	Sidechain              *string    `json:"sidechain"`
	SyncState              string     `json:"sync_state"`
}

// GuardHeldFile is the hold marker of moneroid-guard.service (GUARD-05).
const GuardHeldFile = "/run/moneroid-guard/held"

// GuardState is the content of the hold marker.
type GuardState struct {
	Held   bool       `json:"held"`
	Reason string     `json:"reason"`
	Since  *time.Time `json:"since"`
}

type Health struct {
	Level  string  `json:"level"`
	Issues []Issue `json:"issues"`
}

// Issue is one explainable reason with a stable code (spec §10, contracts §7).
type Issue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"` // info|warning|error
	Component string `json:"component"`
	Message   string `json:"message"`
	Source    string `json:"source,omitempty"`
}

func (s *Snapshot) addIssue(code, severity, component, source, msg string) {
	s.Health.Issues = append(s.Health.Issues, Issue{Code: code, Severity: severity, Component: component, Message: msg, Source: source})
}

// HasIssue reports whether an issue with code was recorded.
func (s *Snapshot) HasIssue(code string) bool {
	for _, i := range s.Health.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
