package agent

import (
	"context"
	"encoding/json"
	"time"
)

func validConsumed(consumed map[string]string) bool {
	if len(consumed) > 100 {
		return false
	}
	for installation, request := range consumed {
		if !validID(installation) || !validID(request) {
			return false
		}
	}
	return true
}
func (lane *packageLane) manualReady(entry packageAssignment, now time.Time) bool {
	if lane.ledger.Cache == nil || !lane.ledger.Cache.ManualSupported || entry.Manual == nil || lane.ledger.Consumed[entry.InstallationID] == entry.Manual.ID {
		return false
	}
	expiry, err := time.Parse(time.RFC3339Nano, entry.Manual.ExpiresAt)
	return err == nil && now.Before(expiry)
}
func (client *Client) startPackageManual(ctx context.Context, state State, run packageRun) (time.Time, error) {
	input := struct {
		SchemaVersion int    `json:"schema_version"`
		ManualID      string `json:"manual_request_id"`
		AssignmentID  string `json:"assignment_id"`
		RunID         string `json:"run_id"`
		Sequence      uint64 `json:"run_sequence"`
	}{1, run.ManualID, run.AssignmentID, run.ID, run.Sequence}
	wire, err := client.post(ctx, "/api/v2/agent/skill-run-starts", state.Credential, input, false, 4096, 4096)
	if err != nil {
		return time.Time{}, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return time.Time{}, err
	}
	var response struct {
		SchemaVersion int    `json:"schema_version"`
		ManualID      string `json:"manual_request_id"`
		AssignmentID  string `json:"assignment_id"`
		RunID         string `json:"run_id"`
		Sequence      uint64 `json:"run_sequence"`
		Deadline      string `json:"run_deadline"`
	}
	if decodeBaseline(body, &response) != nil || response.SchemaVersion != 1 || response.ManualID != run.ManualID || response.AssignmentID != run.AssignmentID || response.RunID != run.ID || response.Sequence != run.Sequence || !validInstant(response.Deadline) {
		return time.Time{}, errPackageState
	}
	deadline, _ := time.Parse(time.RFC3339Nano, response.Deadline)
	if !deadline.After(time.Now()) || deadline.After(time.Now().Add(90*time.Second)) {
		return time.Time{}, errPackageState
	}
	return deadline, nil
}
func (lane *packageLane) executeActive(ctx context.Context, active packageActive) packageCompleted {
	if active.Run.ManualID != "" {
		deadline, err := lane.client.startPackageManual(ctx, lane.state, active.Run)
		if err != nil {
			return packageCompleted{active.Run, false}
		}
		bounded, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		ctx = bounded
	}
	return lane.execute(ctx, active)
}
