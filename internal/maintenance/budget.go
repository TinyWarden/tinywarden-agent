package maintenance

import (
	"context"
	"errors"
	"time"
)

const safePhaseMilliseconds int64 = 30 * 60 * 1000

type bootClock func() (string, int64, error)

// Preparation/fetch cancellation unwinds safe work. Apply deliberately has no
// context or timeout: an elapsed budget never kills a native package transaction.
func phaseBudget(parent context.Context, clock bootClock, interval time.Duration) (context.Context, func() error, context.CancelFunc, error) {
	boot, began, err := clock()
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	check := func() error {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		current, now, err := clock()
		if err != nil || current != boot || now < began {
			return errors.New("maintenance_clock_invalid")
		}
		if now-began >= safePhaseMilliseconds {
			return errors.New("maintenance_phase_timeout")
		}
		return nil
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := check(); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return ctx, check, func() { cancel(context.Canceled) }, nil
}

func safePhase[T any](parent context.Context, run func(context.Context) (T, error)) (T, error) {
	var zero T
	ctx, check, cancel, err := phaseBudget(parent, BootClock, 250*time.Millisecond)
	if err != nil {
		return zero, err
	}
	defer cancel()
	value, err := run(ctx)
	// Recheck on return too: a clock jump must not sneak in between watchdog ticks.
	if budgetErr := check(); budgetErr != nil {
		return zero, budgetErr
	}
	return value, err
}
