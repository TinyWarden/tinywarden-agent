package legacy

import (
	"context"
	"errors"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

type Monitoring interface {
	Epoch() uint64
	Allowed() bool
}
type monitoringControl struct {
	epochFunc   func() uint64
	allowedFunc func() bool
}

func (c *monitoringControl) epoch() uint64 {
	if c == nil {
		return 0
	}
	return c.epochFunc()
}
func (c *monitoringControl) allowed() bool { return c == nil || c.allowedFunc() }

type Workers struct {
	Assignment, Disk, Baseline func(context.Context) error
	Quiescent                  func() bool
	Stop                       func()
}

func NewWorkers(ctx context.Context, store *agentstate.Store, transport *controlplane.Client,
	state *agentstate.State, control Monitoring, known *AssignmentCache, emit func(string)) Workers {
	client := &Client{Client: *transport}
	var monitoring *monitoringControl
	if control != nil {
		monitoring = &monitoringControl{control.Epoch, control.Allowed}
	}
	var err error
	var disk *diskLane
	assignment := func(ctx context.Context) error {
		controlEpoch := monitoring.epoch()
		response, err := client.FetchAssignments(ctx, *state, known)
		if err == nil && controlEpoch != monitoring.epoch() {
			return nil
		}
		if err == nil {
			err = receiveAssignment(store, client.Origin, *state, &known, response)
			if err == nil && disk != nil {
				disk.controlEpoch = controlEpoch
			}
		}
		if err != nil && controlplane.Terminal(err) {
			if pauseErr := pauseAssignmentLease(store, &known); pauseErr != nil {
				emit("assignment_cache_unavailable")
				return errors.Join(err, pauseErr)
			}
		}
		return err
	}
	disk, err = newDiskLane(store, client, state, &known, emit)
	var diskTick func(context.Context) error
	if err != nil {
		emit("disk_queue_unavailable")
	} else {
		disk.monitoring = monitoring
		diskTick = disk.tick
	}
	baseline, err := newBaselineLane(ctx, store, client, *state, emit)
	var baselineTick func(context.Context) error
	if err != nil {
		emit("baseline_state_unavailable")
	} else {
		baseline.monitoring = monitoring
		baselineTick = baseline.tick
	}

	return Workers{Assignment: assignment, Disk: diskTick, Baseline: baselineTick,
		Quiescent: func() bool { return (disk == nil || !disk.collecting) && (baseline == nil || !baseline.running) },
		Stop: func() {
			if baseline != nil {
				baseline.stop()
			}
		}}
}
