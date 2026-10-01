package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func blockBaselinePauseWrite(t *testing.T, lane *baselineLane) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(lane.store.dir, "baseline-pause.json"), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestBaseline401WithPauseWriteFailureStopsAllLanesAndActiveRecipe(t *testing.T) {
	for _, source := range []string{"fetch", "upload"} {
		t.Run(source, func(t *testing.T) {
			server := rejectBaselineServer()
			defer server.Close()
			lane := fixtureLane(t, server)
			started, cancelled := make(chan struct{}), make(chan struct{})
			lane.execute = func(ctx context.Context, active baselineActive) baselineRunRequest {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return interruptedBaseline(active, time.Now())
			}
			if err := lane.tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("recipe did not start")
			}
			blockBaselinePauseWrite(t, lane)
			rejected := &ResponseError{Status: 401, Code: "request_rejected"}
			if source == "fetch" {
				lane.fetch <- baselineFetched{err: rejected}
			} else {
				lane.upload <- uploadedRun{err: rejected}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			heartbeats, assignments, disks, later := 0, 0, 0, 0
			err := runLanes(ctx, 1,
				func(context.Context) error { heartbeats++; return nil },
				func(context.Context) error { assignments++; return nil }, func(string) {},
				func(context.Context) error { disks++; return nil }, lane.tick,
				func(context.Context) error { later++; return nil })
			var response *ResponseError
			var persistence *os.LinkError
			if !errors.As(err, &response) || response != rejected || !errors.As(err, &persistence) ||
				ErrorCategory(err) != "unauthorized" || !lane.paused ||
				heartbeats != 1 || assignments != 1 || disks != 1 || later != 0 {
				t.Fatal("401 did not retain both causes and stop the coordinator", err, heartbeats, assignments, disks, later)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("401 left the active recipe running")
			}
		})
	}
}

func TestBaseline404WithPauseWriteFailureKeepsOtherLanesAvailable(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	lane.available = func() bool { return false }
	blockBaselinePauseWrite(t, lane)
	lane.fetch <- baselineFetched{err: &ResponseError{Status: 404}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unavailable, later := 0, 0
	err := runLanes(ctx, 60, func(context.Context) error { return nil },
		func(context.Context) error { return nil }, func(event string) {
			if event == "baseline_unavailable" {
				unavailable++
			}
		}, func(context.Context) error { return nil }, lane.tick,
		func(context.Context) error { later++; cancel(); return nil })
	if err != context.Canceled || unavailable != 1 || later != 1 || !lane.paused {
		t.Fatal("non-401 fault stopped other lanes", err, unavailable, later)
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
	state := testAssignmentState(t)
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAssignment(readyRecoveryCache(t, server.URL, state)); err != nil {
		t.Fatal(err)
	}
	store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = Run(ctx, Config{ControlPlaneOrigin: server.URL, StateDir: dir}, testClient(server), func(string) {})
	var response *ResponseError
	var persistence *os.LinkError
	if !errors.As(err, &response) || response.Status != 401 || !errors.As(err, &persistence) ||
		heartbeats.Load() != 1 || assignments.Load() != 1 || later.Load() != 0 {
		t.Fatal("assignment 401 was hidden or later authenticated work started", err)
	}
}
