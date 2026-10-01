package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiskQueueRestartSequenceAndScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := State{HostID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		AgentID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Generation: 1}
	queue, sequence, abandoned, err := loadDiskQueue(store, "https://example.org", state)
	if err != nil || abandoned {
		t.Fatal(err)
	}
	first, err := allocateRunSequence(store, &sequence)
	if err != nil || first != 1 {
		t.Fatal("first sequence", err)
	}
	request := DiskRunRequest{SchemaVersion: 1,
		RunID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", RunSequence: first,
		AssignmentID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		StartedAt:    "2026-09-29T12:00:00.000Z", FinishedAt: "2026-09-29T12:00:01.000Z",
		Coverage: "incomplete", Reason: "collector_timeout", Mounts: []DiskMount{}}
	if err := queueRun(store, &queue, request); err != nil {
		t.Fatal(err)
	}
	if err := markInFlight(store, &queue, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"); err == nil {
		t.Fatal("wrong in-flight run was accepted")
	}
	if err := markInFlight(store, &queue, request.RunID); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), queue.Pending[0].Body...)
	loaded, counter, _, err := loadDiskQueue(store, "https://example.org", state)
	if err != nil || loaded.InFlightID != request.RunID ||
		string(loaded.Pending[0].Body) != string(before) || counter.Last != 1 {
		t.Fatal("restart lost ambiguous submission", err)
	}
	if err := acknowledgeRun(store, &loaded, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"); err == nil {
		t.Fatal("wrong receipt removed queue head")
	}
	if err := acknowledgeRun(store, &loaded, request.RunID); err != nil {
		t.Fatal(err)
	}
	loaded, counter, _, err = loadDiskQueue(store, "https://example.org", state)
	if err != nil || len(loaded.Pending) != 0 || loaded.InFlightID != "" {
		t.Fatal("ack failed", err)
	}
	second, err := allocateRunSequence(store, &counter)
	if err != nil || second != 2 {
		t.Fatal("sequence reset after queue drain", err)
	}
	// Removing only the assignment cache cannot reset the durable sequence.
	loaded, counter, _, err = loadDiskQueue(store, "https://example.org", state)
	if err != nil || counter.Last != 2 {
		t.Fatal("sequence lost after cache loss", err)
	}
	request.RunID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	request.RunSequence = second
	if err := queueRun(store, &loaded, request); err != nil {
		t.Fatal(err)
	}
	state.Generation = 2
	newQueue, newSequence, abandoned, err := loadDiskQueue(store, "https://example.org", state)
	if err != nil || !abandoned || len(newQueue.Pending) != 0 || newSequence.Last != 0 {
		t.Fatal("replacement did not isolate pending runs", err)
	}
}

func TestInFlightDiskRequestIsByteIdenticalAfterInterruptedReceipt(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"schema_version":1,"run_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","run_sequence":1,"received_at":"2026-09-29T12:00:03.000Z","duplicate":true}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := State{HostID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		AgentID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Generation: 1}
	queue, sequence, _, err := loadDiskQueue(store, server.URL, state)
	if err != nil {
		t.Fatal(err)
	}
	n, err := allocateRunSequence(store, &sequence)
	if err != nil {
		t.Fatal(err)
	}
	request := DiskRunRequest{SchemaVersion: 1, RunID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		RunSequence: n, AssignmentID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		StartedAt: "2026-09-29T12:00:00.000Z", FinishedAt: "2026-09-29T12:00:01.000Z",
		Coverage: "incomplete", Reason: "collector_timeout", Mounts: []DiskMount{}}
	if err := queueRun(store, &queue, request); err != nil {
		t.Fatal(err)
	}
	if err := markInFlight(store, &queue, request.RunID); err != nil {
		t.Fatal(err)
	}
	client := testClient(server)
	if err := client.PostDiskRun(context.Background(), "synthetic", queue.Pending[0]); err == nil {
		t.Fatal("first failed receipt was acknowledged")
	}
	restarted, _, _, err := loadDiskQueue(store, server.URL, state)
	if err != nil || restarted.InFlightID != request.RunID {
		t.Fatal("in-flight run was lost", err)
	}
	if err := client.PostDiskRun(context.Background(), "synthetic", restarted.Pending[0]); err != nil {
		t.Fatal("exact retry failed", err)
	}
	if len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) ||
		string(bodies[0]) != string(queue.Pending[0].Body) {
		t.Fatal("retry body changed")
	}
}

func TestBlockedDiskCollectionDoesNotStarveHeartbeat(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := State{HostID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		AgentID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Generation: 1,
		HeartbeatIntervalSeconds: 1}
	known := &AssignmentCache{ID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		ValidatedAt: time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")}
	known.Assignment.Applicability = "ready"
	known.Assignment.Effective.IntervalSeconds = 60
	client := &Client{Origin: "https://example.org"}
	lane, err := newDiskLane(store, client, &state, &known, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	lane.collect = func(ctx context.Context) CollectorResult {
		close(started)
		<-ctx.Done()
		return failedCollection("collector_failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2300*time.Millisecond)
	defer cancel()
	var heartbeats atomic.Int32
	finished := make(chan error, 1)
	go func() {
		finished <- runLanes(ctx, 1,
			func(context.Context) error { heartbeats.Add(1); return nil },
			func(context.Context) error { return nil }, func(string) {}, lane.tick)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("collector did not start")
	}
	err = <-finished
	if !errors.Is(err, context.DeadlineExceeded) || heartbeats.Load() < 2 {
		t.Fatalf("blocked disk starved heartbeat: beats=%d err=%v", heartbeats.Load(), err)
	}
}

func TestDiskQueueCorruptionOverflowAndLease(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := State{HostID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		AgentID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Generation: 1}
	queue, seq, _, err := loadDiskQueue(store, "https://example.org", state)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		n, err := allocateRunSequence(store, &seq)
		if err != nil {
			t.Fatal(err)
		}
		id, err := randomID()
		if err != nil {
			t.Fatal(err)
		}
		request := DiskRunRequest{SchemaVersion: 1, RunID: id, RunSequence: n,
			AssignmentID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
			StartedAt:    "2026-09-29T12:00:00.000Z", FinishedAt: "2026-09-29T12:00:01.000Z",
			Coverage: "incomplete", Reason: "collector_failed", Mounts: []DiskMount{}}
		if err := queueRun(store, &queue, request); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := markInFlight(store, &queue, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(queue.Pending) != 100 || queue.DroppedRuns != 1 || queue.Pending[0].ID != queue.InFlightID {
		t.Fatal("oldest non-flight run was not discarded")
	}
	data, err := os.ReadFile(filepath.Join(store.dir, "disk-queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	pending := raw["pending"].([]any)
	pending[0].(map[string]any)["digest"] = "0"
	modified, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(store.dir, "disk-queue.json"), modified, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadDiskQueue(store, "https://example.org", state); err == nil {
		t.Fatal("corrupt queue accepted")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	cache := &AssignmentCache{ValidatedAt: now.Format("2006-01-02T15:04:05.000Z")}
	cache.Assignment.Applicability = "ready"
	if !assignmentLease(cache, now) || assignmentLease(cache, now.Add(24*time.Hour)) ||
		assignmentLease(cache, now.Add(-time.Millisecond)) {
		t.Fatal("lease bound failed")
	}
}
