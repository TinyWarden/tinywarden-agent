package agent

import (
	"context"
	"errors"
)

func Run(ctx context.Context, config Config, client *Client, emit func(string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	store, err := OpenStore(config.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return err
	}
	if err := store.UpgradePending(&state); err != nil {
		return err
	}
	if state.Enrollment != nil {
		if err := runCycle(ctx, func(ctx context.Context) error {
			return finishEnrollment(ctx, store, &state, client)
		}, emit); err != nil {
			return err
		}
	}
	if state.Replacement != nil {
		if err := runCycle(ctx, func(ctx context.Context) error {
			return finishReplacement(ctx, store, &state, client)
		}, emit); err != nil {
			return err
		}
	}
	known, err := store.LoadAssignment(client.Origin, state)
	if err != nil {
		emit("assignment_cache_unavailable")
		known = nil
	}
	heartbeat := func(ctx context.Context) error { return sendHeartbeat(ctx, store, &state, client) }
	assignment := func(ctx context.Context) error {
		response, err := client.FetchAssignments(ctx, state, known)
		if err == nil {
			err = receiveAssignment(store, client.Origin, state, &known, response)
		}
		if err != nil && terminal(err) {
			if pauseErr := pauseAssignmentLease(store, &known); pauseErr != nil {
				emit("assignment_cache_unavailable")
				return errors.Join(err, pauseErr)
			}
		}
		return err
	}
	disk, err := newDiskLane(store, client, &state, &known, emit)
	var diskTick func(context.Context) error
	if err != nil {
		emit("disk_queue_unavailable")
	} else {
		diskTick = disk.tick
	}
	baseline, err := newBaselineLane(ctx, store, client, state, emit)
	var baselineTick func(context.Context) error
	if err != nil {
		emit("baseline_state_unavailable")
	} else {
		baselineTick = baseline.tick
		defer baseline.stop()
	}
	return runLanes(ctx, state.HeartbeatIntervalSeconds, heartbeat, assignment, emit, diskTick, baselineTick)
}
