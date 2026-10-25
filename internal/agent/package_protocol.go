package agent

import (
	"context"
	"encoding/json"
	"slices"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func (client *Client) fetchPackages(ctx context.Context, state agentstate.State, ready bool, resources ...bool) (*packageResponse, error) {
	if len(resources) > 0 && resources[0] {
		ctx = context.WithValue(ctx, resourceFeatureContext{}, true)
	}
	sdk3 := len(resources) > 1 && resources[1]
	if sdk3 {
		ctx = context.WithValue(ctx, controlplane.SDK3FeatureContext{}, true)
	}
	wire, err := client.Request(ctx, "/api/v2/agent/skill-assignments", state.Credential,
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
	if controlplane.Decode(body, &response) != nil || !validPackageResponse(agentstate.ScopeFor(client.Origin, state), response) {
		return nil, errPackageState
	}
	if slices.Contains(response.Features, "skill_packages.sdk3.v1") && !sdk3 {
		return nil, errPackageState
	}
	return &response, nil
}
func (client *Client) uploadPackage(ctx context.Context, state agentstate.State, pending packagePending) error {
	var run packageRun
	if controlplane.Decode(pending.Body, &run) != nil {
		return errPackageState
	}
	wire, err := client.Request(ctx, "/api/v2/agent/skill-runs", state.Credential, json.RawMessage(pending.Body), false, 1<<20, 4096)
	if err != nil {
		return err
	}
	var id string
	var sequence uint64
	var at string
	if json.Unmarshal(wire["run_id"], &id) != nil || json.Unmarshal(wire["run_sequence"], &sequence) != nil || json.Unmarshal(wire["received_at"], &at) != nil || id != run.ID || sequence != run.Sequence || !agentstate.ValidInstant(at) {
		return errPackageState
	}
	return nil
}

type resourceFeatureContext = controlplane.ResourceFeatureContext
