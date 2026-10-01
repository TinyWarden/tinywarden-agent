package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func readyRecoveryCache(t *testing.T, origin string, state State) AssignmentCache {
	t.Helper()
	cache := testAssignment(t, origin, state)
	cache.Assignment.Applicability = "ready"
	cache.Assignment.Checks = []AssignedCheck{{Kind: "disk_usage"}}
	data, err := json.Marshal([]any{1, cache.HostID, cache.AgentID, cache.Generation,
		"disk-local", cache.Revision, 1, 0, "inherit", "ready", "disk_usage.v1",
		1, 1, 85, 95, 300, 10, 900})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	cache.Digest = hex.EncodeToString(digest[:])
	cache.ValidatedAt = time.Now().UTC().Truncate(time.Millisecond).
		Format("2006-01-02T15:04:05.000Z")
	if !validAssignment(cache) {
		t.Fatal("invalid ready cache")
	}
	return cache
}

func TestTerminalAssignmentRejectionPersistsPauseAcrossRestartAndOutage(t *testing.T) {
	var assignmentStatus atomic.Int32
	assignmentStatus.Store(409)
	var runCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/baseline-assignments":
			w.WriteHeader(404)
		case "/api/v1/agent/heartbeat":
			var input struct {
				Sequence uint64 `json:"sequence"`
				SentAt   string `json:"sent_at"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"schema_version":1,"sequence":%d,"accepted_at":%q,"duplicate":false,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`,
				input.Sequence, input.SentAt)
		case "/api/v1/agent/assignments":
			w.WriteHeader(int(assignmentStatus.Load()))
		case "/api/v1/agent/disk-runs":
			runCalls.Add(1)
			w.WriteHeader(503)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := testAssignmentState(t)
	cache := readyRecoveryCache(t, server.URL, state)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAssignment(cache); err != nil {
		t.Fatal(err)
	}
	store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = Run(ctx, Config{ControlPlaneOrigin: server.URL, StateDir: dir}, testClient(server),
		func(event string) {
			if event == "assignment_unavailable" {
				cancel()
			}
		})
	if err != context.Canceled {
		t.Fatal("terminal response did not stop assignment lane", err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := store.LoadAssignment(server.URL, state)
	store.Close()
	if err != nil || paused == nil || paused.ValidatedAt != "" ||
		paused.ID != cache.ID || paused.Revision != cache.Revision || paused.Digest != cache.Digest {
		t.Fatal("terminal fault did not durably revoke lease and preserve authority", err)
	}
	assignmentStatus.Store(503)
	restartCtx, stop := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer stop()
	err = Run(restartCtx, Config{ControlPlaneOrigin: server.URL, StateDir: dir},
		testClient(server), func(string) {})
	if err != context.DeadlineExceeded || runCalls.Load() != 0 {
		t.Fatal("restart resumed disk with failed assignment fetch", err, runCalls.Load())
	}
	sequence, err := os.ReadFile(filepath.Join(dir, "disk-sequence.json"))
	if err != nil {
		t.Fatal(err)
	}
	var counter diskSequence
	if json.Unmarshal(sequence, &counter) != nil || counter.Last != 0 {
		t.Fatal("restart collected a run under an invalidated lease")
	}
}
