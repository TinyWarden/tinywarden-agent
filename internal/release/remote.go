package release

const RemoteUpdateCapability = "agent_update_requests.v1"

type RemoteUpdate struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Error     *string   `json:"error"`
	ExpiresAt string    `json:"expires_at"`
	Candidate Candidate `json:"candidate"`
}
type RemoteUpdateRequest struct {
	Schema     int     `json:"schema_version"`
	HostID     string  `json:"host_id"`
	AgentID    string  `json:"agent_id"`
	Generation uint64  `json:"generation"`
	RequestID  *string `json:"request_id"`
	State      string  `json:"state"`
	Error      *string `json:"error"`
}
type RemoteUpdateReply struct {
	Schema     int           `json:"schema_version"`
	HostID     string        `json:"host_id"`
	AgentID    string        `json:"agent_id"`
	Generation uint64        `json:"generation"`
	Capability string        `json:"capability"`
	Request    *RemoteUpdate `json:"request"`
}
