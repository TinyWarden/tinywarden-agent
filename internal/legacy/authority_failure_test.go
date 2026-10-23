package legacy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	loop "github.com/TinyWarden/tinywarden-agent/internal/agent/loop"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func blockBaselinePauseWrite(t *testing.T, lane *baselineLane) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(lane.store.Dir(), "baseline-pause.json"), 0700); err != nil {
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
			rejected := &controlplane.ResponseError{Status: 401, Code: "request_rejected"}
			if source == "fetch" {
				lane.fetch <- baselineFetched{err: rejected}
			} else {
				lane.upload <- uploadedRun{err: rejected}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			heartbeats, assignments, disks, later := 0, 0, 0, 0
			err := loop.RunLanes(ctx, 1,
				func(context.Context) error { heartbeats++; return nil },
				func(context.Context) error { assignments++; return nil }, func(string) {},
				func(context.Context) error { disks++; return nil }, lane.tick,
				func(context.Context) error { later++; return nil })
			var response *controlplane.ResponseError
			var persistence *os.LinkError
			if !errors.As(err, &response) || response != rejected || !errors.As(err, &persistence) ||
				controlplane.ErrorCategory(err) != "unauthorized" || !lane.paused ||
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
	lane.fetch <- baselineFetched{err: &controlplane.ResponseError{Status: 404}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unavailable, later := 0, 0
	err := loop.RunLanes(ctx, 60, func(context.Context) error { return nil },
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
