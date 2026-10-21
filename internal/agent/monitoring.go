package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

type monitoringState struct {
	Scope       baselineScope `json:"scope"`
	Epoch       uint64        `json:"epoch"`
	Mode        string        `json:"mode"`
	IssuedAt    string        `json:"issued_at"`
	ValidUntil  string        `json:"valid_until"`
	ValidatedAt string        `json:"validated_at"`
}
type monitoringResponse struct {
	SchemaVersion int    `json:"schema_version"`
	HostID        string `json:"host_id"`
	AgentID       string `json:"agent_id"`
	Generation    string `json:"generation"`
	Epoch         string `json:"epoch"`
	Mode          string `json:"mode"`
	IssuedAt      string `json:"issued_at"`
	ValidUntil    string `json:"valid_until"`
	PollInterval  int    `json:"poll_interval_seconds"`
}
type monitoringResult struct {
	response monitoringResponse
	err      error
}
type monitoringLane struct {
	store                          *Store
	client                         *Client
	state                          State
	saved                          monitoringState
	now                            func() time.Time
	emit                           func(string)
	quiescent                      func() bool
	result                         chan monitoringResult
	fetching, legacy, invalidClock bool
	nextFetch, lastWall            time.Time
}

func validMonitoring(s monitoringState) bool {
	if !s.Scope.valid() || s.Epoch < 1 || s.Epoch > 9007199254740991 || (s.Mode != "active" && s.Mode != "paused") || !validInstant(s.IssuedAt) || !validInstant(s.ValidUntil) || !validInstant(s.ValidatedAt) {
		return false
	}
	issued, _ := time.Parse(time.RFC3339Nano, s.IssuedAt)
	until, _ := time.Parse(time.RFC3339Nano, s.ValidUntil)
	return until.After(issued) && until.Sub(issued) <= 5*time.Minute
}
func newMonitoringLane(store *Store, client *Client, state State, emit func(string)) (*monitoringLane, error) {
	scope := scopeBaseline(client.Origin, state)
	var saved monitoringState
	found, err := readBaselineState(store, "monitoring.json", 4096, &saved)
	if err != nil {
		return nil, err
	}
	if found && !validMonitoring(saved) {
		return nil, errBaselineState
	}
	if found && saved.Scope != scope {
		if err := archiveBaselineState(store, "monitoring.json"); err != nil {
			return nil, err
		}
		saved = monitoringState{}
	}
	lane := &monitoringLane{store: store, client: client, state: state, saved: saved, now: time.Now, emit: emit, result: make(chan monitoringResult, 1), quiescent: func() bool { return true }}
	if saved.Epoch > 0 {
		lane.lastWall, _ = time.Parse(time.RFC3339Nano, saved.ValidatedAt)
	}
	return lane, nil
}
func (lane *monitoringLane) epoch() uint64 {
	if lane == nil {
		return 0
	}
	return lane.saved.Epoch
}
func (lane *monitoringLane) allowed() bool {
	if lane == nil {
		return true
	}
	if lane.invalidClock {
		return false
	}
	if lane.saved.Epoch == 0 {
		return lane.legacy
	}
	if lane.saved.Mode != "active" {
		return false
	}
	now := lane.now()
	issued, _ := time.Parse(time.RFC3339Nano, lane.saved.IssuedAt)
	until, _ := time.Parse(time.RFC3339Nano, lane.saved.ValidUntil)
	validated, _ := time.Parse(time.RFC3339Nano, lane.saved.ValidatedAt)
	return !now.Before(validated) && !now.Before(issued.Add(-2*time.Minute)) && now.Before(until) && now.Sub(validated) < until.Sub(issued)
}
func (lane *monitoringLane) tick(ctx context.Context) error {
	now := lane.now()
	wall := now.UTC()
	if !lane.lastWall.IsZero() && wall.Before(lane.lastWall) {
		lane.invalidClock = true
		lane.nextFetch = now
	}
	lane.lastWall = wall
	select {
	case result := <-lane.result:
		lane.fetching = false
		lane.nextFetch = now.Add(time.Minute)
		if result.err != nil {
			var response *ResponseError
			if errors.As(result.err, &response) && response.Status == 401 {
				return result.err
			}
			if lane.saved.Epoch == 0 && errors.As(result.err, &response) && response.Status == 404 {
				lane.legacy = true
			} else {
				lane.emit("monitoring_control_deferred")
			}
		} else {
			r := result.response
			epoch, err := strconv.ParseUint(r.Epoch, 10, 64)
			saved := monitoringState{Scope: scopeBaseline(lane.client.Origin, lane.state), Epoch: epoch, Mode: r.Mode, IssuedAt: r.IssuedAt, ValidUntil: r.ValidUntil, ValidatedAt: baselineStamp(now)}
			issued, _ := time.Parse(time.RFC3339Nano, r.IssuedAt)
			if err != nil || r.SchemaVersion != 1 || r.HostID != lane.state.HostID || r.AgentID != lane.state.AgentID || r.Generation != strconv.FormatUint(lane.state.Generation, 10) || r.PollInterval != 60 || !validMonitoring(saved) || epoch < lane.saved.Epoch || epoch == lane.saved.Epoch && lane.saved.Epoch > 0 && r.Mode != lane.saved.Mode || issued.After(now.Add(2*time.Minute)) || issued.Before(now.Add(-2*time.Minute)) {
				lane.invalidClock = true
				lane.emit("monitoring_control_invalid")
			} else {
				if err := saveBaselineState(lane.store, "monitoring.json", saved, 4096); err != nil {
					return err
				}
				lane.saved = saved
				lane.legacy = false
				lane.invalidClock = false
			}
		}
	default:
	}
	if !lane.fetching && !now.Before(lane.nextFetch) {
		var applied any
		if lane.saved.Epoch > 0 && (lane.saved.Mode == "active" || lane.quiescent()) {
			applied = struct {
				Epoch string `json:"epoch"`
				Mode  string `json:"mode"`
			}{strconv.FormatUint(lane.saved.Epoch, 10), lane.saved.Mode}
		}
		input := struct {
			SchemaVersion int      `json:"schema_version"`
			Capabilities  []string `json:"capabilities"`
			Applied       any      `json:"applied"`
		}{1, []string{"monitoring_control.v1"}, applied}
		lane.fetching = true
		go func() {
			wire, err := lane.client.post(ctx, "/api/v1/agent/monitoring-control", lane.state.Credential, input, false, 4096, 4096)
			var response monitoringResponse
			if err == nil {
				body, e := json.Marshal(wire)
				err = e
				if err == nil {
					err = decodeBaseline(body, &response)
				}
			}
			select {
			case lane.result <- monitoringResult{response, err}:
			case <-ctx.Done():
			}
		}()
	}
	return nil
}
