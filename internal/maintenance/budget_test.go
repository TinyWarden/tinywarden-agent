package maintenance

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafePhaseBudgetIncludesSuspendAndCancelsOnlySafeWork(t *testing.T) {
	var now atomic.Int64
	clock := func() (string, int64, error) { return "same-boot", now.Load(), nil }
	ctx, check, cancel, err := phaseBudget(context.Background(), clock, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	now.Store(safePhaseMilliseconds)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("safe phase was not cancelled after a suspended-clock jump")
	}
	if err = check(); err == nil || err.Error() != "maintenance_phase_timeout" {
		t.Fatal("elapsed safe phase accepted", err)
	}
}
func TestSafePhaseBudgetChecksCompletionBetweenTicksAndClockRegression(t *testing.T) {
	var now atomic.Int64
	ctx, check, cancel, err := phaseBudget(context.Background(), func() (string, int64, error) { return "same", now.Load(), nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	now.Store(safePhaseMilliseconds)
	if ctx.Err() != nil {
		t.Fatal("watchdog ran before completion check")
	}
	if err = check(); err == nil || err.Error() != "maintenance_phase_timeout" {
		t.Fatal("late completion accepted", err)
	}
	now.Store(-1)
	if err = check(); err == nil || err.Error() != "maintenance_clock_invalid" {
		t.Fatal("regressed clock accepted", err)
	}
}
func TestSafePhaseBudgetPreservesCallerCancellationAndBootIdentity(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	var changed atomic.Bool
	ctx, check, cancel, err := phaseBudget(parent, func() (string, int64, error) {
		if changed.Load() {
			return "other", 1, nil
		}
		return "same", 0, nil
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	changed.Store(true)
	if err = check(); err == nil || err.Error() != "maintenance_clock_invalid" {
		t.Fatal("changed boot accepted", err)
	}
	stop()
	if !errors.Is(check(), context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("caller cancellation not preserved")
	}
}
