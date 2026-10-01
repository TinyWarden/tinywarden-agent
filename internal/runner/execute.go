package runner

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type engine struct {
	slot        chan struct{}
	platform    func() bool
	supervisor  func() *exec.Cmd
	kill        func(int) error
	groupAbsent func(int) bool
	wait        func(*exec.Cmd) error
}

var processRunner = newEngine()

func newEngine() *engine {
	return &engine{
		slot: make(chan struct{}, 1), platform: platformAllowed,
		supervisor: func() *exec.Cmd {
			path, err := os.Executable()
			if err != nil {
				return exec.Command("")
			}
			return exec.Command(path, SupervisorArgument)
		},
		kill: func(pid int) error {
			if pid <= 1 {
				return errPolicy
			}
			return syscall.Kill(-pid, syscall.SIGKILL)
		},
		groupAbsent: func(pid int) bool { return pid > 1 && syscall.Kill(-pid, 0) == syscall.ESRCH },
		wait:        func(cmd *exec.Cmd) error { return cmd.Wait() },
	}
}

// Execute is synchronous for its worker caller, never for the heartbeat loop.
// The shared process slot survives a bounded return with incomplete cleanup.
func Execute(ctx context.Context, encoded []byte) Result {
	return processRunner.execute(ctx, encoded)
}

// Availability is an admission hint; Execute still owns the atomic slot claim.
// A worker uses it before allocating an identity while old cleanup holds the slot.
func Available() bool { return len(processRunner.slot) == 0 }

func (e *engine) execute(ctx context.Context, encoded []byte) Result {
	recipe, err := Parse(encoded)
	if err != nil || !e.platform() {
		return Result{Outcome: PolicyRejected}
	}
	select {
	case e.slot <- struct{}{}:
	default:
		return Result{Outcome: RunnerBusy}
	}
	release := true
	defer func() {
		if release {
			<-e.slot
		}
	}()
	deadline := time.Now().Add(time.Duration(recipe.TimeoutSeconds) * time.Second)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result := Result{Outcome: Exited}
	for _, step := range recipe.Steps {
		if interrupted(ctx) {
			result.Outcome = interruption(ctx)
			break
		}
		value, held := e.runStep(ctx, step, deadline)
		result.Steps = append(result.Steps, value)
		result.Outcome = value.Outcome
		if held {
			release = false
		}
		if held || value.Outcome != Exited || value.Signal != nil {
			break
		}
	}
	return result
}

func interruption(ctx context.Context) Outcome {
	if ctx.Err() == context.DeadlineExceeded || (ctx.Err() == nil && deadlinePassed(ctx)) {
		return TimedOut
	}
	return Cancelled
}

func deadlinePassed(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func interrupted(ctx context.Context) bool {
	return ctx.Err() != nil || deadlinePassed(ctx)
}
