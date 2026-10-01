package agent

import (
	"context"
	"encoding/json"
)

type baselineHint struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
}

func (client *Client) fetchBaseline(ctx context.Context, state State, known *baselineCache, capabilities []string) (*baselineResponse, error) {
	hints := make([]any, 3)
	for i, key := range baselineKeys {
		var hint *baselineHint
		if known != nil {
			e := known.Entries[i]
			hint = &baselineHint{e.ID, e.Revision, e.Digest}
		}
		hints[i] = map[string]any{"definition_key": key, "known": hint}
	}
	wire, err := client.post(ctx, "/api/v1/agent/baseline-assignments", state.Credential, map[string]any{
		"schema_version": 1, "agent_version": Version, "capabilities": capabilities, "known_assignments": hints}, false, maxBody, baselineBytes)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	var r baselineResponse
	if decodeBaseline(data, &r) != nil {
		return nil, errBaselineState
	}
	if _, err := resolveBaselineResponse(client.Origin, state, known, r); err != nil {
		return nil, err
	}
	return &r, nil
}

func resolveBaselineResponse(origin string, state State, known *baselineCache, r baselineResponse) (baselineCache, error) {
	scope := scopeBaseline(origin, state)
	c := baselineCache{Scope: scope, Entries: make([]baselineEntry, 3)}
	if r.SchemaVersion != 1 || r.HostID != scope.HostID || r.AgentID != scope.AgentID || r.Generation != scope.Generation ||
		r.Poll != 60 || len(r.Entries) != 3 || known != nil && (known.Scope != scope || !validBaselineCache(*known)) {
		return c, errBaselineState
	}
	for i, key := range baselineKeys {
		e := r.Entries[i]
		if e.Key != key {
			return c, errBaselineState
		}
		if known != nil {
			old := known.Entries[i]
			if e.Revision < old.Revision || e.Revision == old.Revision && (e.ID != old.ID || e.Digest != old.Digest) {
				return c, errBaselineState
			}
		}
		if e.NotModified {
			if known == nil || e.Assignment != nil {
				return c, errBaselineState
			}
			old := known.Entries[i]
			if e.ID != old.ID || e.Revision != old.Revision || e.Digest != old.Digest {
				return c, errBaselineState
			}
			e = old
		}
		if !validBaselineEntry(scope, e) {
			return c, errBaselineState
		}
		c.Entries[i] = e
	}
	return c, nil
}

func (client *Client) postBaselineRun(ctx context.Context, credential string, item baselineQueued) error {
	wire, err := client.post(ctx, "/api/v1/agent/baseline-runs", credential, json.RawMessage(item.Body), false, 32<<10, maxBody)
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
	if decodeBaseline(data, &receipt) != nil || receipt.SchemaVersion != 1 || receipt.ID != item.ID || receipt.Sequence != item.Sequence || !validInstant(receipt.ReceivedAt) {
		return errBaselineState
	}
	return nil
}
