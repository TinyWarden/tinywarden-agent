package legacy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/TinyWarden/tinywarden-agent/internal/agent"
	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	legacylane "github.com/TinyWarden/tinywarden-agent/internal/legacy"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func TestTerminalAssignmentRejectionPersistsPauseAcrossRestartAndOutage(t *testing.T) {
	var assignmentStatus atomic.Int32
	assignmentStatus.Store(409)
	var runCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/baseline-assignments", "/api/v1/agent/monitoring-control":
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
	state := legacylane.TestAssignmentState(t)
	cache := legacylane.TestReadyCache(t, server.URL, state)
	store, err := agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := legacylane.SaveAssignment(store, cache); err != nil {
		t.Fatal(err)
	}
	store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = agent.Run(ctx, agentconfig.Config{ControlPlaneOrigin: server.URL, StateDir: dir}, testClient(server),
		func(event string) {
			if event == "assignment_unavailable" {
				cancel()
			}
		})
	if err != context.Canceled {
		t.Fatal("terminal response did not stop assignment lane", err)
	}
	store, err = agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := legacylane.LoadAssignment(store, server.URL, state)
	store.Close()
	if err != nil || paused == nil || paused.ValidatedAt != "" ||
		paused.ID != cache.ID || paused.Revision != cache.Revision || paused.Digest != cache.Digest {
		t.Fatal("terminal fault did not durably revoke lease and preserve authority", err)
	}
	assignmentStatus.Store(503)
	restartCtx, stop := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer stop()
	err = agent.Run(restartCtx, agentconfig.Config{ControlPlaneOrigin: server.URL, StateDir: dir},
		testClient(server), func(string) {})
	if err != context.DeadlineExceeded || runCalls.Load() != 0 {
		t.Fatal("restart resumed disk with failed assignment fetch", err, runCalls.Load())
	}
	sequence, err := os.ReadFile(filepath.Join(dir, "disk-sequence.json"))
	if err != nil {
		t.Fatal(err)
	}
	var counter struct{ Last uint64 }
	if json.Unmarshal(sequence, &counter) != nil || counter.Last != 0 {
		t.Fatal("restart collected a run under an invalidated lease")
	}
}
func TestDiskAssignment401WithPauseWriteFailureStopsRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var heartbeats, assignments, later atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/heartbeat":
			heartbeats.Add(1)
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
			assignments.Add(1)
			path := filepath.Join(dir, "assignments.json")
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Error(err)
			}
			w.WriteHeader(401)
		default:
			later.Add(1)
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	state := legacylane.TestAssignmentState(t)
	store, err := agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := legacylane.SaveAssignment(store, legacylane.TestReadyCache(t, server.URL, state)); err != nil {
		t.Fatal(err)
	}
	store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = agent.Run(ctx, agentconfig.Config{ControlPlaneOrigin: server.URL, StateDir: dir}, testClient(server), func(string) {})
	var response *controlplane.ResponseError
	var persistence *os.LinkError
	if !errors.As(err, &response) || response.Status != 401 || !errors.As(err, &persistence) ||
		heartbeats.Load() != 1 || assignments.Load() != 1 || later.Load() != 0 {
		t.Fatal("assignment 401 was hidden or later authenticated work started", err)
	}
}
func TestAssignmentEndpointAbsenceDoesNotStopHeartbeat(t *testing.T) {
	state := legacylane.TestAssignmentState(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	heartbeatCalls, assignmentCalls := 0, 0
	events := make(chan string, 4)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agent/baseline-assignments" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/api/v1/agent/assignments" {
			assignmentCalls++
			w.WriteHeader(404)
			events <- "assignment"
			return
		}
		if r.URL.Path != "/api/v1/agent/heartbeat" {
			t.Error("unexpected path")
			w.WriteHeader(404)
			return
		}
		heartbeatCalls++
		var input struct {
			Sequence uint64 `json:"sequence"`
			SentAt   string `json:"sent_at"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"schema_version":1,"sequence":%d,"accepted_at":%q,"duplicate":false,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`,
			input.Sequence, input.SentAt)
		events <- "heartbeat"
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-events; <-events; cancel() }()
	err = agent.Run(ctx, agentconfig.Config{ControlPlaneOrigin: server.URL, StateDir: dir}, testClient(server), func(string) {})
	if err != context.Canceled || heartbeatCalls < 1 || assignmentCalls < 1 {
		t.Fatal(err, heartbeatCalls, assignmentCalls)
	}
}

func testClient(server *httptest.Server) *agent.Client {
	c := legacylane.TestClient(server)
	return &agent.Client{Client: c.Client}
}
