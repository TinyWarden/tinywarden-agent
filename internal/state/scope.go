package state

const MaxSafeCounter = 9007199254740991

type Scope struct {
	Version    int    `json:"version"`
	Origin     string `json:"origin"`
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
}

func ScopeFor(origin string, state State) Scope {
	return Scope{1, origin, state.HostID, state.AgentID, state.Generation}
}
func (s Scope) Valid() bool {
	return s.Version == 1 && s.Origin != "" && ValidID(s.HostID) && ValidID(s.AgentID) && s.Generation >= 1 && s.Generation <= MaxSafeCounter
}
