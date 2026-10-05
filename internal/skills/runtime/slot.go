package runtime

import "context"

// Shared by generic and compiled observations. It never occupies the heartbeat loop.
var executionSlot = make(chan struct{}, 1)

func TryAcquire() (func(), bool) {
	select {
	case executionSlot <- struct{}{}:
		return func() { <-executionSlot }, true
	default:
		return nil, false
	}
}

func Acquire(ctx context.Context) (func(), error) {
	select {
	case executionSlot <- struct{}{}:
		return func() { <-executionSlot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
