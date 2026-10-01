package agent

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestAssignmentBackoffKeepsHeartbeatPriorityAndPendingReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		order := []string{}
		heartbeats, assignments := 0, 0
		pending := false
		err := runLanes(ctx, 10, func(context.Context) error {
			order = append(order, "heartbeat")
			heartbeats++
			if heartbeats == 1 {
				pending = true
				return &ResponseError{Status: 503}
			}
			if heartbeats == 2 {
				if !pending {
					t.Error("assignment retry discarded pending heartbeat")
				}
				pending = false
			}
			if heartbeats == 4 {
				cancel()
			}
			return nil
		}, func(context.Context) error {
			order = append(order, "assignment")
			assignments++
			return &ResponseError{Status: 503}
		}, func(string) {})
		if err != context.Canceled || heartbeats != 4 || assignments < 2 || pending ||
			len(order) < 2 || order[0] != "heartbeat" || order[1] != "assignment" {
			t.Fatal("lanes did not stay independent", err, heartbeats, assignments, order)
		}
	})
}
