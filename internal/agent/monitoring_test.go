package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func monitoringFixture(t *testing.T) (*monitoringLane, context.Context) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(server.Close)
	state := testAssignmentState(t)
	lane, err := newMonitoringLane(baselineStore(t), testClient(server), state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return lane, ctx
}
func offerMonitoring(lane *monitoringLane, epoch uint64, mode string) {
	now := lane.now()
	lane.fetching = true
	lane.result <- monitoringResult{response: monitoringResponse{SchemaVersion: 1, HostID: lane.state.HostID, AgentID: lane.state.AgentID, Generation: strconv.FormatUint(lane.state.Generation, 10), Epoch: strconv.FormatUint(epoch, 10), Mode: mode, IssuedAt: controlplane.Timestamp(now), ValidUntil: controlplane.Timestamp(now.Add(5 * time.Minute)), PollInterval: 60}}
}
func TestMonitoringPauseIsDurableAndCannotExpireOrRegressIntoActive(t *testing.T) {
	lane, ctx := monitoringFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	lane.now = func() time.Time { return now }
	offerMonitoring(lane, 2, "paused")
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lane.Allowed() {
		t.Fatal("paused collection")
	}
	restarted, err := newMonitoringLane(lane.store, lane.client, lane.state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	restarted.now = func() time.Time { return now }
	if restarted.Allowed() {
		t.Fatal("saved pause expired into active")
	}
	offerMonitoring(restarted, 1, "active")
	if err := restarted.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.Allowed() || restarted.saved.Epoch != 2 {
		t.Fatal("stale response restored collection")
	}
}
func TestMonitoringRequiresFreshLeaseAndFailsClosedAfterNegotiation(t *testing.T) {
	lane, ctx := monitoringFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	lane.now = func() time.Time { return now }
	if lane.Allowed() {
		t.Fatal("collection before negotiation")
	}
	lane.fetching = true
	lane.result <- monitoringResult{err: &controlplane.ResponseError{Status: 404}}
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if !lane.Allowed() {
		t.Fatal("old app fallback unavailable")
	}
	offerMonitoring(lane, 1, "active")
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if !lane.Allowed() {
		t.Fatal("fresh lease rejected")
	}
	now = now.Add(5 * time.Minute)
	if lane.Allowed() {
		t.Fatal("expired lease usable")
	}
	lane.fetching = true
	lane.result <- monitoringResult{err: &controlplane.ResponseError{Status: 404}}
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lane.Allowed() {
		t.Fatal("downgrade reopened collection")
	}
}
func TestMonitoringModeCannotChangeWithoutNewEpoch(t *testing.T) {
	lane, ctx := monitoringFixture(t)
	offerMonitoring(lane, 3, "paused")
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	offerMonitoring(lane, 3, "active")
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lane.Allowed() || lane.saved.Mode != "paused" {
		t.Fatal("mode changed in immutable epoch")
	}
	offerMonitoring(lane, 4, "active")
	if err := lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if !lane.Allowed() {
		t.Fatal("new active epoch rejected")
	}
}

func TestPauseAcknowledgementWaitsForCollectionQuiescence(t *testing.T) {
	requests := make(chan json.RawMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Applied json.RawMessage `json:"applied"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		requests <- body.Applied
		w.WriteHeader(503)
	}))
	defer server.Close()
	state := testAssignmentState(t)
	lane, err := newMonitoringLane(baselineStore(t), testClient(server), state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quiescent := false
	lane.quiescent = func() bool { return quiescent }
	offerMonitoring(lane, 2, "paused")
	if err = lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	lane.nextFetch = time.Time{}
	if err = lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-requests:
		if string(body) != "null" {
			t.Fatalf("acknowledged running collection: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("control request missing")
	}
	select {
	case <-lane.result:
		lane.fetching = false
	case <-time.After(time.Second):
		t.Fatal("control response missing")
	}
	quiescent = true
	lane.nextFetch = time.Time{}
	if err = lane.tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-requests:
		var ack struct {
			Epoch string `json:"epoch"`
			Mode  string `json:"mode"`
		}
		if json.Unmarshal(body, &ack) != nil || ack.Epoch != "2" || ack.Mode != "paused" {
			t.Fatalf("missing durable paused ack: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("quiescent request missing")
	}
}

func TestPauseCancelsPackageCollectionAndResumeRequiresFreshAssignment(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	packages := packageFixture(t, server)
	control, err := newMonitoringLane(packages.store, packages.client, packages.state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	offerMonitoring(control, 2, "paused")
	if err = control.tick(ctx); err != nil {
		t.Fatal(err)
	}
	cancelled := false
	packages.monitoring, packages.controlEpoch, packages.running = control, 1, true
	packages.cancelRun = func() { cancelled = true }
	defer func() { packages.running = false }()
	if err = packages.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("pause did not cancel package collector")
	}
	packages.running = false
	offerMonitoring(control, 3, "active")
	if err = control.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err = packages.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if packages.running {
		t.Fatal("resumed with assignments fetched under the old control epoch")
	}
}
