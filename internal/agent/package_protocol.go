package agent

import (
	"context"
	"encoding/json"
)

func (client *Client) fetchPackages(ctx context.Context, state State, ready bool) (*packageResponse, error) {
	wire, err := client.post(ctx, "/api/v2/agent/skill-assignments", state.Credential,
		struct {
			SchemaVersion int  `json:"schema_version"`
			RuntimeReady  bool `json:"runtime_ready"`
		}{1, ready}, false, 4096, 1<<20)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	var response packageResponse
	if decodeBaseline(body, &response) != nil || !validPackageResponse(scopeBaseline(client.Origin, state), response) {
		return nil, errPackageState
	}
	return &response, nil
}
func (client *Client) uploadPackage(ctx context.Context, state State, pending packagePending) error {
	var run packageRun
	if decodeBaseline(pending.Body, &run) != nil {
		return errPackageState
	}
	wire, err := client.post(ctx, "/api/v2/agent/skill-runs", state.Credential, json.RawMessage(pending.Body), false, 1<<20, 4096)
	if err != nil {
		return err
	}
	var id string
	var sequence uint64
	var at string
	if json.Unmarshal(wire["run_id"], &id) != nil || json.Unmarshal(wire["run_sequence"], &sequence) != nil || json.Unmarshal(wire["received_at"], &at) != nil || id != run.ID || sequence != run.Sequence || !validInstant(at) {
		return errPackageState
	}
	return nil
}
