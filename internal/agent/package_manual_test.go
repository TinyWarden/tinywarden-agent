package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func manualServer(t *testing.T, fail bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/agent/skill-run-starts" {
			w.WriteHeader(503)
			return
		}
		if fail {
			w.WriteHeader(503)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid claim")
		}
		body["run_deadline"] = baselineStamp(time.Now().Add(90 * time.Second))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}
func prepareManual(lane *packageLane, now time.Time) packageAssignment {
	lane.ledger.Cache.ManualSupported = true
	id, _ := randomID()
	lane.ledger.Cache.Assignments[0].Manual = &packageManualRequest{id, baselineStamp(now.Add(5 * time.Minute))}
	for _, entry := range lane.ledger.Cache.Assignments {
		lane.due[entry.InstallationID] = now.Add(time.Hour)
	}
	return lane.ledger.Cache.Assignments[0]
}
func TestManualEarlyCadenceClaimAndCachedRequestConsumed(t *testing.T) {
	server := manualServer(t, false)
	defer server.Close()
	lane := packageFixture(t, server)
	now := time.Now()
	entry := prepareManual(lane, now)
	due := lane.due[entry.InstallationID]
	var count atomic.Int32
	lane.execute = func(_ context.Context, a packageActive) packageCompleted {
		count.Add(1)
		if a.Run.ManualID != entry.Manual.ID {
			t.Error("lost manual correlation")
		}
		return packageCompleted{a.Run, false}
	}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	settlePackage(t, lane, func() bool { return len(lane.ledger.Pending) == 1 })
	if count.Load() != 1 || !lane.due[entry.InstallationID].Equal(due) {
		t.Fatal("manual moved schedule")
	}
	for range 20 {
		if err := lane.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if count.Load() != 1 {
		t.Fatal("cached manual ran twice")
	}
	loaded, err := loadPackageLedger(lane.store, lane.ledger.Scope)
	if err != nil || loaded.Consumed[entry.InstallationID] != entry.Manual.ID {
		t.Fatal("consumption not durable", err)
	}
}
func TestManualLostClaimReplyNeverCollectsAndRestartRetainsFailure(t *testing.T) {
	server := manualServer(t, true)
	defer server.Close()
	lane := packageFixture(t, server)
	entry := prepareManual(lane, time.Now())
	var count atomic.Int32
	lane.execute = func(_ context.Context, a packageActive) packageCompleted {
		count.Add(1)
		return packageCompleted{a.Run, false}
	}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	settlePackage(t, lane, func() bool { return len(lane.ledger.Pending) == 1 })
	if count.Load() != 0 {
		t.Fatal("executed without acknowledgement")
	}
	var failed packageRun
	_ = json.Unmarshal(lane.ledger.Pending[0].Body, &failed)
	if failed.ManualID != entry.Manual.ID || failed.Outcome != "execution_failed" {
		t.Fatal("uncertain claim lost correlation")
	}
	loaded, err := loadPackageLedger(lane.store, lane.ledger.Scope)
	if err != nil || string(loaded.Pending[0].Body) != string(lane.ledger.Pending[0].Body) {
		t.Fatal("restart changed failure", err)
	}
}
func TestManualRestartBeforeOrAfterClaimUsesSameIdentity(t *testing.T) {
	server := manualServer(t, false)
	defer server.Close()
	for _, claimed := range []bool{false, true} {
		lane := packageFixture(t, server)
		entry := prepareManual(lane, time.Now())
		active, err := lane.allocate(entry, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if claimed {
			if _, err = lane.client.startPackageManual(context.Background(), lane.state, active.Run); err != nil {
				t.Fatal(err)
			}
		}
		loaded, err := loadPackageLedger(lane.store, lane.ledger.Scope)
		if err != nil {
			t.Fatal(err)
		}
		var run packageRun
		_ = json.Unmarshal(loaded.Pending[0].Body, &run)
		if run.ID != active.Run.ID || run.Sequence != active.Run.Sequence || run.ManualID != entry.Manual.ID || run.Outcome != "execution_failed" {
			t.Fatal("restart reused or lost manual request")
		}
		again, err := loadPackageLedger(lane.store, lane.ledger.Scope)
		if err != nil || string(again.Pending[0].Body) != string(loaded.Pending[0].Body) {
			t.Fatal("failure replay changed", err)
		}
	}
}
func TestManualDueCoalescesAndFairWorkerPreservesBackpressure(t *testing.T) {
	server := manualServer(t, false)
	defer server.Close()
	lane := packageFixture(t, server)
	now := time.Now()
	entry := prepareManual(lane, now)
	for _, e := range lane.ledger.Cache.Assignments {
		lane.due[e.InstallationID] = now.Add(-time.Second)
	}
	started := make(chan string, 3)
	lane.execute = func(_ context.Context, a packageActive) packageCompleted {
		started <- a.Entry.InstallationID
		return packageCompleted{a.Run, false}
	}
	_ = lane.tick(context.Background())
	settlePackage(t, lane, func() bool { return len(lane.ledger.Pending) == 3 })
	if !lane.due[entry.InstallationID].After(now) || len(started) != 3 || lane.ledger.Sequences[entry.InstallationID] != 1 {
		t.Fatal("manual did not coalesce")
	}
	for _, expected := range lane.ledger.Cache.Assignments {
		if <-started != expected.InstallationID {
			t.Fatal("unfair rotation")
		}
	}
	for len(lane.ledger.Pending) < 8 {
		lane.ledger.Pending = append(lane.ledger.Pending, lane.ledger.Pending[0])
	}
	next, _ := randomID()
	lane.ledger.Cache.Assignments[0].Manual.ID = next
	_ = lane.tick(context.Background())
	if lane.running {
		t.Fatal("manual bypassed backpressure")
	}
}
func TestManualNegotiatedHeaderAndOlderServerResponse(t *testing.T) {
	var response *packageResponse
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TinyWarden-Capabilities") != "skill_runs.manual.v1" {
			t.Error("capability missing")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 2 {
			t.Error("changed old request JSON")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	lane := packageFixture(t, server)
	response = lane.ledger.Cache
	wire, err := lane.client.fetchPackages(context.Background(), lane.state, true)
	if err != nil || wire.ManualSupported {
		t.Fatal("old server compatibility failed", err)
	}
	wire.Assignments[0].Manual = &packageManualRequest{ID: "bad", ExpiresAt: baselineStamp(time.Now())}
	if validPackageResponse(lane.ledger.Scope, *wire) {
		t.Fatal("accepted invalid unnegotiated manual request")
	}
}
