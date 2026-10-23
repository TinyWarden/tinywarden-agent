package legacy

import (
	"context"
	"encoding/json"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

type baselineHint struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
}

func (client *Client) fetchBaseline(ctx context.Context, state agentstate.State, known *baselineCache, capabilities []string) (*baselineResponse, error) {
	hints := make([]any, 3)
	for i, key := range baselineKeys {
		var hint *baselineHint
		if known != nil {
			e := known.Entries[i]
			hint = &baselineHint{e.ID, e.Revision, e.Digest}
		}
		hints[i] = map[string]any{"definition_key": key, "known": hint}
	}
	wire, err := client.Request(ctx, "/api/v1/agent/baseline-assignments", state.Credential, map[string]any{
		"schema_version": 1, "agent_version": Version, "capabilities": capabilities, "known_assignments": hints}, false, controlplane.MaxBody, baselineBytes)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	var r baselineResponse
	if controlplane.Decode(data, &r) != nil {
		return nil, controlplane.ErrInvalidState
	}
	if _, err := resolveBaselineResponse(client.Origin, state, known, r); err != nil {
		return nil, err
	}
	return &r, nil
}

func resolveBaselineResponse(origin string, state agentstate.State, known *baselineCache, r baselineResponse) (baselineCache, error) {
	scope := agentstate.ScopeFor(origin, state)
	c := baselineCache{Scope: scope, Entries: make([]baselineEntry, 3)}
	if r.SchemaVersion != 1 || r.HostID != scope.HostID || r.AgentID != scope.AgentID || r.Generation != scope.Generation ||
		r.Poll != 60 || len(r.Entries) != 3 || known != nil && (known.Scope != scope || !validBaselineCache(*known)) {
		return c, controlplane.ErrInvalidState
	}
	for i, key := range baselineKeys {
		e := r.Entries[i]
		if e.Key != key {
			return c, controlplane.ErrInvalidState
		}
		if known != nil {
			old := known.Entries[i]
			if e.Revision < old.Revision || e.Revision == old.Revision && (e.ID != old.ID || e.Digest != old.Digest) {
				return c, controlplane.ErrInvalidState
			}
		}
		if e.NotModified {
			if known == nil || e.Assignment != nil {
				return c, controlplane.ErrInvalidState
			}
			old := known.Entries[i]
			if e.ID != old.ID || e.Revision != old.Revision || e.Digest != old.Digest {
				return c, controlplane.ErrInvalidState
			}
			e = old
		}
		if !validBaselineEntry(scope, e) {
			return c, controlplane.ErrInvalidState
		}
		c.Entries[i] = e
	}
	return c, nil
}

func (client *Client) postBaselineRun(ctx context.Context, credential string, item baselineQueued) error {
	wire, err := client.Request(ctx, "/api/v1/agent/baseline-runs", credential, json.RawMessage(item.Body), false, 32<<10, controlplane.MaxBody)
	if err != nil {
		return err
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	var receipt struct {
		SchemaVersion int    `json:"schema_version"`
		ID            string `json:"run_id"`
		Sequence      uint64 `json:"run_sequence"`
		ReceivedAt    string `json:"received_at"`
		Duplicate     bool   `json:"duplicate"`
	}
	if controlplane.Decode(data, &receipt) != nil || receipt.SchemaVersion != 1 || receipt.ID != item.ID || receipt.Sequence != item.Sequence || !agentstate.ValidInstant(receipt.ReceivedAt) {
		return controlplane.ErrInvalidState
	}
	return nil
}
