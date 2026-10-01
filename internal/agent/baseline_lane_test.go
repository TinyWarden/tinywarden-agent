package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/TinyWarden/tinywarden/agent/internal/baseline"
)

func fixtureLane(t *testing.T, server *httptest.Server) *baselineLane {
	t.Helper()
	store := baselineStore(t)
	state, cache := baselineFixture(t, server.URL)
	if _, _, err := loadBaselineWork(store, cache.Scope, false); err != nil {
		t.Fatal(err)
	}
	if err := saveBaselineCache(store, cache); err != nil {
		t.Fatal(err)
	}
	lane, err := newBaselineLane(context.Background(), store, testClient(server), state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lane.stop)
	lane.nextFetch, lane.nextUpload = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	lane.capabilities = func() []string { return []string{"exec_observe.debian13.v1"} }
	return lane
}
func rejectBaselineServer() *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
}
func settleBaseline(t *testing.T, lane *baselineLane, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		if err := lane.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !done() {
		t.Fatal("baseline work did not settle")
	}
}

func TestBaselineFairWorkerNoCatchupAndBusySlotDefersAllocation(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	now := time.Now()
	lane.now = func() time.Time { return now }
	lane.available = func() bool { return false }
	if err := lane.tick(context.Background()); err != nil || lane.sequence.Last != 0 {
		t.Fatal("held slot allocated work", err)
	}
	lane.available = func() bool { return true }
	keys := make(chan baseline.Key, 6)
	unblock := make(chan struct{})
	lane.execute = func(ctx context.Context, a baselineActive) baselineRunRequest {
		keys <- a.Entry.Key
		select {
		case <-unblock:
		case <-ctx.Done():
		}
		return interruptedBaseline(a, now)
	}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := lane.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if lane.sequence.Last != 1 || len(keys) > 1 {
		t.Fatal("ticks queued concurrent work")
	}
	unblock <- struct{}{}
	settleBaseline(t, lane, func() bool { return lane.sequence.Last == 2 })
	unblock <- struct{}{}
	settleBaseline(t, lane, func() bool { return lane.sequence.Last == 3 })
	unblock <- struct{}{}
	settleBaseline(t, lane, func() bool { return !lane.running })
	if <-keys != baseline.Packages || <-keys != baseline.Reboot || <-keys != baseline.Fstrim {
		t.Fatal("worker starved a key")
	}
	for range 20 {
		if err := lane.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if lane.sequence.Last != 3 {
		t.Fatal("catch-up burst")
	}
}

func TestBaselineLeaseDeadlineCancelsAndBackwardWallPauseSurvivesRestart(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	now := time.Now()
	lane.now = func() time.Time { return now }
	lane.cache.ValidatedAt = baselineStamp(now.Add(-24*time.Hour + 2*time.Second))
	started := make(chan time.Time, 1)
	cancelled := make(chan struct{}, 1)
	lane.execute = func(ctx context.Context, a baselineActive) baselineRunRequest {
		deadline, _ := ctx.Deadline()
		started <- deadline
		<-ctx.Done()
		cancelled <- struct{}{}
		return interruptedBaseline(a, time.Now())
	}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deadline := <-started; deadline.After(now.Add(2 * time.Second)) {
		t.Fatal("execution escaped remaining lease")
	}
	now = now.Add(3 * time.Second)
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("expired lease left active execution")
	}
	now = now.Add(-time.Second)
	validated := lane.cache.ValidatedAt
	response := baselineReply(lane.state, cloneEntries(t, lane.cache.Entries))
	lane.fetch <- baselineFetched{response: &response}
	if err := lane.tick(context.Background()); err != nil || !lane.paused {
		t.Fatal("backward wall did not pause", err)
	}
	if !lane.cache.Paused || lane.cache.ValidatedAt != validated {
		t.Fatal("in-flight fetch renewed a clock-paused lease")
	}
	lane.stop()
	restarted, err := newBaselineLane(context.Background(), lane.store, lane.client, lane.state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stop()
	if !restarted.paused {
		t.Fatal("restart cleared durable clock pause")
	}
}

func TestBaseline404PausesBeforeFirstCacheAndDoesNotStopOtherLanes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(404) }))
	defer server.Close()
	store := baselineStore(t)
	state := testAssignmentState(t)
	lane, err := newBaselineLane(context.Background(), store, testClient(server), state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer lane.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2300*time.Millisecond)
	defer cancel()
	hb, disk := 0, 0
	err = runLanes(ctx, 1, func(context.Context) error { hb++; return nil }, func(context.Context) error { return nil }, func(string) {},
		func(context.Context) error { disk++; return nil }, lane.tick)
	if err != context.DeadlineExceeded || hb < 2 || disk < 2 || calls.Load() != 1 {
		t.Fatal("endpoint skew stopped contact/disk", err, hb, disk, calls.Load())
	}
	lane.stop()
	restarted, err := newBaselineLane(context.Background(), store, lane.client, state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stop()
	if !restarted.paused {
		t.Fatal("404 pause disappeared before cache existed")
	}
	if err := restarted.tick(context.Background()); err != nil || restarted.fetching {
		t.Fatal("restart retried terminal endpoint", err)
	}
}

func TestBaseline503RetriesExactRequestThenDrainsWithoutLease(t *testing.T) {
	var calls atomic.Int32
	bodies := make(chan string, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		json.NewDecoder(r.Body).Decode(&raw)
		bodies <- string(raw)
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		var req baselineRunRequest
		json.Unmarshal(raw, &req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "run_id": req.ID, "run_sequence": req.Sequence, "received_at": stamp(), "duplicate": true})
	}))
	defer server.Close()
	lane := fixtureLane(t, server)
	a, err := allocateBaseline(lane.store, &lane.sequence, lane.cache.Entries[0], stamp(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := finishBaseline(lane.store, &lane.queue, &lane.sequence, interruptedBaseline(*a, time.Now())); err != nil {
		t.Fatal(err)
	}
	original := lane.queue.Pending[0]
	lane.cache.ValidatedAt = baselineStamp(time.Now().Add(-25 * time.Hour))
	lane.nextUpload = time.Time{}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	settleBaseline(t, lane, func() bool { return !lane.uploading })
	if lane.queue.Pending[0] != original || lane.queue.InFlightID != original.ID {
		t.Fatal("ambiguous body changed")
	}
	lane.nextUpload = time.Time{}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	settleBaseline(t, lane, func() bool { return len(lane.queue.Pending) == 0 })
	if <-bodies != original.Body || <-bodies != original.Body {
		t.Fatal("retry changed bytes")
	}
}

func TestBaseline401StopsCoordinatorAndHeartbeatPriority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		order := []string{}
		err := runLanes(context.Background(), 1, func(context.Context) error { order = append(order, "heartbeat"); return nil },
			func(context.Context) error { order = append(order, "disk-assignment"); return nil }, func(string) {},
			func(context.Context) error { order = append(order, "disk"); return nil },
			func(context.Context) error { order = append(order, "baseline"); return &ResponseError{Status: 401} })
		if ErrorCategory(err) != "unauthorized" || len(order) != 4 || order[0] != "heartbeat" || order[3] != "baseline" {
			t.Fatal("authority/priority", err, order)
		}
	})
}
