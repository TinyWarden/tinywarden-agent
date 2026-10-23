package controlplane

import (
	"encoding/json"

	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func ReadState(store *agentstate.Store, name string, limit int, target any) (bool, error) {
	var raw json.RawMessage
	found, err := agentstate.ReadFile(store, name, limit, &raw)
	if err != nil || !found {
		return found, err
	}
	if len(raw) > limit || Decode(raw, target) != nil {
		return true, ErrInvalidState
	}
	return true, nil
}
