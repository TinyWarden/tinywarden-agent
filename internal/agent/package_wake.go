package agent

import (
	"errors"
	"time"

	loop "github.com/TinyWarden/tinywarden-agent/internal/agent/loop"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

type packageHint struct {
	hint    controlplane.WorkHint
	err     error
	elapsed time.Duration
}

// Optional hints have their own asynchronous transport. Failed/old endpoints
// cannot delay normal discovery, the heartbeat or result delivery.
func (lane *packageLane) pollHints(now time.Time) {
	if !lane.waitEnabled {
		return
	}
	select {
	case result := <-lane.work:
		lane.waiting = false
		if result.err != nil {
			lane.waitFailures++
			delay := 30 * time.Second
			var response *controlplane.ResponseError
			if errors.As(result.err, &response) && response.RetryAfter > delay {
				delay = response.RetryAfter
			}
			lane.nextWait = now.Add(delay)
			lane.report("work_wait", result.err, lane.waitFailures, result.elapsed, delay)
		} else {
			if lane.waitFailures > 0 {
				lane.report("work_wait", nil, lane.waitFailures+1, result.elapsed, 0)
			}
			if result.hint.Available || lane.waitFailures > 0 {
				lane.fetchAgain = lane.fetching
				// Never shorten an explicit Retry-After from the owning route.
				lane.nextFetch = maxInstant(now, lane.fetchNotBefore)
				lane.nextUpload = maxInstant(now, lane.uploadNotBefore)
			}
			lane.cursor = &result.hint.Cursor
			lane.waitFailures = 0
			lane.nextWait = now
		}
	default:
	}
	if !lane.waiting && !now.Before(lane.nextWait) {
		lane.waiting = true
		cursor := lane.cursor
		go func() {
			started := time.Now()
			hint, err := lane.waitClient.WaitForWork(lane.ctx, lane.state.Credential, cursor, "monitoring")
			lane.work <- packageHint{hint, err, time.Since(started)}
		}()
	}
}

func maxInstant(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func retryNotBefore(now time.Time, err error) time.Time {
	var response *controlplane.ResponseError
	if errors.As(err, &response) && response.RetryAfter > 0 {
		return now.Add(response.RetryAfter)
	}
	return time.Time{}
}

func (lane *packageLane) report(kind string, err error, attempt int, elapsed, retry time.Duration) {
	if lane.diagnostic != nil {
		lane.diagnostic(loop.NewMonitoringDiagnostic(kind, len(lane.ledger.Pending), err, attempt, elapsed, retry))
	}
}

// Manual requests precede periodic work, preserving round-robin order within
// each class. Existing work is never cancelled merely to start a manual check.
func (lane *packageLane) nextEntry(now time.Time) *packageAssignment {
	entries := lane.ledger.Cache.Assignments
	for _, manual := range []bool{true, false} {
		for offset := 0; offset < len(entries); offset++ {
			index := (lane.rotate + offset) % len(entries)
			entry := entries[index]
			if entry.Applicability != "ready" || manual && !lane.manualReady(entry, now) ||
				!manual && now.Before(lane.due[entry.InstallationID]) {
				continue
			}
			lane.rotate = (index + 1) % len(entries)
			return &entry
		}
	}
	return nil
}
