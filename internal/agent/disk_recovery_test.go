package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func recoveryQueue(t *testing.T, origin string) (*Store, State, diskQueue, []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := State{HostID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		AgentID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Generation: 1,
		Credential: "synthetic"}
	queue, sequence, _, err := loadDiskQueue(store, origin, state)
	if err != nil {
		t.Fatal(err)
	}
	n, err := allocateRunSequence(store, &sequence)
	if err != nil {
		t.Fatal(err)
	}
	request := DiskRunRequest{SchemaVersion: 1,
		RunID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", RunSequence: n,
		AssignmentID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		StartedAt:    "2026-09-29T12:00:00.000Z", FinishedAt: "2026-09-29T12:00:01.000Z",
		Coverage: "incomplete", Reason: "collector_timeout", Mounts: []DiskMount{}}
	if err := queueRun(store, &queue, request); err != nil {
		t.Fatal(err)
	}
	return store, state, queue, append([]byte(nil), queue.Pending[0].Body...)
}

func settleDiskUpload(t *testing.T, lane *diskLane) error {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for lane.uploading && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if err := lane.tick(context.Background()); err != nil {
			return err
		}
	}
	if lane.uploading {
		t.Fatal("disk upload did not settle")
	}
	return nil
}

func TestDiskOutageReconnectKeepsExactHeadAndDrainsWithExpiredLease(t *testing.T) {
	var calls atomic.Int32
	bodies := make(chan []byte, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- body
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"schema_version":1,"run_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","run_sequence":1,"received_at":"2026-09-29T12:00:03.000Z","duplicate":true}`))
	}))
	defer server.Close()
	store, state, _, original := recoveryQueue(t, server.URL)
	defer store.Close()
	cache := &AssignmentCache{ValidatedAt: time.Now().Add(-25 * time.Hour).UTC().Truncate(time.Millisecond).
		Format("2006-01-02T15:04:05.000Z")}
	cache.Assignment.Applicability = "ready"
	known := cache
	lane, err := newDiskLane(store, testClient(server), &state, &known, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	lane.collect = func(context.Context) CollectorResult {
		t.Error("expired lease collected a new run")
		return failedCollection("collector_failed")
	}
	if err := lane.tick(context.Background()); err != nil || !lane.uploading {
		t.Fatal("outage upload did not start", err)
	}
	if err := settleDiskUpload(t, lane); err != nil || len(lane.queue.Pending) != 1 ||
		lane.queue.InFlightID != lane.queue.Pending[0].ID ||
		string(lane.queue.Pending[0].Body) != string(original) {
		t.Fatal("503 lost ambiguous request", err)
	}
	lane.nextUpload = time.Now().Add(-time.Second)
	if err := lane.tick(context.Background()); err != nil || !lane.uploading {
		t.Fatal("reconnect upload did not start", err)
	}
	if err := settleDiskUpload(t, lane); err != nil || len(lane.queue.Pending) != 0 {
		t.Fatal("exact retry did not drain queue", err)
	}
	if calls.Load() != 2 || string(<-bodies) != string(original) ||
		string(<-bodies) != string(original) {
		t.Fatal("reconnect changed request body")
	}
	loaded, _, _, err := loadDiskQueue(store, server.URL, state)
	if err != nil || len(loaded.Pending) != 0 {
		t.Fatal("ack was not durable", err)
	}
}

func TestRollbackRejectionPausesDiskButPreservesHeartbeatAndHead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"invalid_request", 400}, {"lost_snapshot", 409}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, heartbeats atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			store, state, _, original := recoveryQueue(t, server.URL)
			defer store.Close()
			var known *AssignmentCache
			events := []string{}
			lane, err := newDiskLane(store, testClient(server), &state, &known, func(event string) {
				events = append(events, event)
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2400*time.Millisecond)
			defer cancel()
			err = runLanes(ctx, 1,
				func(context.Context) error { heartbeats.Add(1); return nil },
				func(context.Context) error { return nil },
				func(event string) { events = append(events, event) }, lane.tick)
			if err != context.DeadlineExceeded || heartbeats.Load() < 2 || calls.Load() != 1 ||
				!strings.Contains(strings.Join(events, ","), "disk_unavailable") {
				t.Fatal("rejection did not pause only disk lane", err, heartbeats.Load(), calls.Load(), events)
			}
			loaded, _, _, err := loadDiskQueue(store, server.URL, state)
			if err != nil || len(loaded.Pending) != 1 || loaded.InFlightID != loaded.Pending[0].ID ||
				string(loaded.Pending[0].Body) != string(original) {
				t.Fatal("rejection lost queued evidence", err)
			}
		})
	}
}

func TestDiskQueueEnforcesCombinedByteLimitAndReportsDrops(t *testing.T) {
	store, state, queue, _ := recoveryQueue(t, "https://example.org")
	defer store.Close()
	if err := markInFlight(store, &queue, queue.Pending[0].ID); err != nil {
		t.Fatal(err)
	}
	_, sequence, _, err := loadDiskQueue(store, "https://example.org", state)
	if err != nil {
		t.Fatal(err)
	}
	zero, hundred := "0", "100"
	mounts := make([]DiskMount, 128)
	for i := range mounts {
		mounts[i] = DiskMount{MountID: i + 1, MountPath: "/" + strings.Repeat("p", 1000),
			MountRoot: "/" + strings.Repeat("r", 1000), FilesystemType: "ext4", Kind: "local",
			Writable: true, Reason: "none", TotalBytes: &hundred, FreeBytes: &zero,
			AvailableBytes: &zero}
	}
	for i := 0; i < 40; i++ {
		n, err := allocateRunSequence(store, &sequence)
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
			Coverage: "complete", Reason: "none", DroppedRuns: queue.DroppedRuns, Mounts: mounts}
		if err := queueRun(store, &queue, request); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(filepath.Join(store.dir, "disk-queue.json"))
	if err != nil || info.Size() > maxPendingBytes || len(queue.Pending) >= 41 ||
		queue.DroppedRuns == 0 || queue.Pending[0].ID != queue.InFlightID {
		t.Fatal("combined byte limit failed", err)
	}
	var latest DiskRunRequest
	if err := json.Unmarshal(queue.Pending[len(queue.Pending)-1].Body, &latest); err != nil ||
		latest.DroppedRuns == 0 {
		t.Fatal("drop count not reported in later request", err)
	}
}
