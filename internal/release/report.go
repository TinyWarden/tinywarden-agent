package release

type UpdateSettings struct {
	Automatic bool   `json:"automatic"`
	HostID    string `json:"host_id"`
	AgentID   string `json:"agent_id"`
}
type UpdateStatus struct {
	Outcome   string `json:"outcome"`
	CheckedAt string `json:"checked_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Target    string `json:"target,omitempty"`
	Error     string `json:"error,omitempty"`
}
type Observation struct {
	Version          string       `json:"version"`
	Managed          bool         `json:"managed"`
	BinarySHA256     string       `json:"binary_sha256"`
	RuntimeSHA256    string       `json:"runtime_sha256"`
	DescriptorSHA256 string       `json:"descriptor_sha256"`
	InstalledVersion string       `json:"installed_version"`
	LocalAutomatic   bool         `json:"local_automatic"`
	RemoteUpdates    bool         `json:"remote_updates,omitempty"`
	Updater          UpdateStatus `json:"updater"`
}
type PolicyRequest struct {
	Schema     int    `json:"schema_version"`
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
	Version    string `json:"version"`
	Managed    bool   `json:"managed"`
}
type Candidate struct {
	Manifest  string `json:"manifest"`
	Signature string `json:"signature"`
}
type Policy struct {
	Schema          int        `json:"schema_version"`
	HostID          string     `json:"host_id"`
	AgentID         string     `json:"agent_id"`
	Generation      uint64     `json:"generation"`
	Watermark       uint64     `json:"watermark"`
	Allowed         bool       `json:"allowed"`
	Reason          string     `json:"reason"`
	ExpiresAt       string     `json:"expires_at"`
	CatalogAt       string     `json:"catalog_at"`
	DefaultRevision uint64     `json:"default_revision"`
	HostRevision    uint64     `json:"host_revision"`
	Capabilities    []string   `json:"capabilities"`
	Candidate       *Candidate `json:"candidate"`
	Reported        *Evidence  `json:"reported"`
}
type Evidence struct {
	ObservedAt       string `json:"observed_at"`
	Version          string `json:"version"`
	BinarySHA256     string `json:"binary_sha256"`
	RuntimeSHA256    string `json:"runtime_sha256"`
	DescriptorSHA256 string `json:"descriptor_sha256"`
}
type StatusRequest struct {
	Schema     int         `json:"schema_version"`
	HostID     string      `json:"host_id"`
	AgentID    string      `json:"agent_id"`
	Generation uint64      `json:"generation"`
	Sequence   uint64      `json:"sequence"`
	ObservedAt string      `json:"observed_at"`
	Platform   Observation `json:"platform"`
}

var outcomes = map[string]bool{"idle": true, "checking": true, "disabled": true, "available": true, "installing": true, "awaiting_confirmation": true, "updated": true, "failed": true, "manual_required": true, "app_required": true}

func ValidOutcome(s string) bool { return outcomes[s] }
