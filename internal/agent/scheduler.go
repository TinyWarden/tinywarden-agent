package agent

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Both lanes get one bounded attempt when due. Heartbeat always runs first.
func runLanes(ctx context.Context, heartbeatInterval int,
	heartbeatAttempt, assignmentAttempt func(context.Context) error, emit func(string),
	laneTicks ...func(context.Context) error) error {
	heartbeatDue := time.Now()
	assignmentDue := time.Now()
	heartbeatFailures, assignmentFailures := 0, 0
	assignmentEnabled := true
	enabled := make([]bool, len(laneTicks))
	for i, tick := range laneTicks {
		enabled[i] = tick != nil
	}
	retryDelay := func(err error, failures int) time.Duration {
		delays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
		if failures <= len(delays) {
			return retryWait(err, delays[failures-1])
		}
		return degradedWait(err)
	}
	for {
		if !time.Now().Before(heartbeatDue) {
			err := heartbeatAttempt(ctx)
			if err == nil {
				if heartbeatFailures > 4 {
					emit("recovered")
				}
				heartbeatFailures = 0
				jitter := time.Duration(rand.Int64N(int64(heartbeatInterval)*100_000_000 + 1))
				heartbeatDue = time.Now().Add(time.Duration(heartbeatInterval)*time.Second + jitter)
			} else {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if terminal(err) {
					return err
				}
				heartbeatFailures++
				if heartbeatFailures == 5 {
					emit("degraded")
				}
				heartbeatDue = time.Now().Add(retryDelay(err, heartbeatFailures))
			}
		}
		if assignmentEnabled && !time.Now().Before(assignmentDue) {
			err := assignmentAttempt(ctx)
			if err == nil {
				if assignmentFailures > 0 {
					emit("assignment_recovered")
				}
				assignmentFailures = 0
				assignmentDue = time.Now().Add(60 * time.Second)
			} else {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var response *ResponseError
				if errors.As(err, &response) && response.Status == 401 {
					return err
				}
				if terminal(err) {
					assignmentEnabled = false
					emit("assignment_unavailable")
				} else {
					assignmentFailures++
					if assignmentFailures == 5 {
						emit("assignment_degraded")
					}
					assignmentDue = time.Now().Add(retryDelay(err, assignmentFailures))
				}
			}
		}
		for i, tick := range laneTicks {
			if !enabled[i] {
				continue
			}
			if err := tick(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var response *ResponseError
				if errors.As(err, &response) && response.Status == 401 {
					return err
				}
				if i == 0 {
					emit("disk_unavailable")
				} else {
					emit("baseline_unavailable")
				}
				enabled[i] = false
			}
		}
		deadline := heartbeatDue
		if assignmentEnabled && assignmentDue.Before(deadline) {
			deadline = assignmentDue
		}
		for _, active := range enabled {
			if !active {
				continue
			}
			poll := time.Now().Add(time.Second)
			if poll.Before(deadline) {
				deadline = poll
			}
		}
		if delay := time.Until(deadline); delay > 0 {
			if err := wait(ctx, delay); err != nil {
				return err
			}
		}
	}
}
