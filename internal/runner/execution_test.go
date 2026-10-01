package runner

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

func TestDirectCommandEvidence(t *testing.T) {
	for _, mode := range []string{"success", "nonzero", "signal", "raw", "early", "malformed", "control-flood", "forged"} {
		t.Run(mode, func(t *testing.T) {
			result := testEngine(mode).execute(context.Background(), recipeBytes(2, 3))
			wanted := Exited
			if mode == "early" || mode == "malformed" || mode == "control-flood" || mode == "forged" {
				wanted = HelperFailed
			}
			if result.Outcome != wanted {
				t.Fatalf("outcome=%s", result.Outcome)
			}
			step := result.Steps[0]
			if !step.CleanupComplete || step.Duration <= 0 {
				t.Fatal("missing cleanup/duration evidence")
			}
			switch mode {
			case "signal":
				if step.Signal == nil || *step.Signal != 15 || step.ExitCode != nil || len(result.Steps) != 1 {
					t.Fatal("signal coerced into success")
				}
			case "success", "nonzero", "raw":
				code := 0
				if mode == "nonzero" {
					code = 7
				}
				if step.ExitCode == nil || *step.ExitCode != code || step.Signal != nil || len(result.Steps) != 2 {
					t.Fatal("command exit confused with supervisor SIGKILL")
				}
				if mode == "success" && (string(step.Stdout) != "out" || string(step.Stderr) != "err") {
					t.Fatal("stream evidence lost")
				}
			default:
				if step.ExitCode != nil || step.Signal != nil || len(result.Steps) != 1 {
					t.Fatal("bad control interpreted as command completion")
				}
			}
		})
	}
}

func TestDeadlineCancellationAndIndependentWork(t *testing.T) {
	for _, deadline := range []bool{true, false} {
		e := testEngine("sleep")
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan Result, 1)
		go func() { result <- e.execute(ctx, recipeBytes(2, 1)) }()
		awaitSlot(t, e)
		// Injected heartbeat ticks advance while the runner owns a sleeping child.
		// Concurrent requests are rejected immediately and are never queued.
		for tick := 0; tick < 20; tick++ {
			if r := e.execute(context.Background(), recipeBytes(1, 1)); r.Outcome != RunnerBusy {
				t.Fatalf("tick %d: %s", tick, r.Outcome)
			}
		}
		if !deadline {
			cancel()
		}
		r := <-result
		cancel()
		wanted := TimedOut
		if !deadline {
			wanted = Cancelled
		}
		if r.Outcome != wanted || len(r.Steps) != 1 || r.Steps[0].ExitCode != nil || !r.Steps[0].CleanupComplete {
			t.Fatalf("interruption=%+v", r)
		}
		if r.Steps[0].Duration > 2*time.Second {
			t.Fatal("interruption exceeded cleanup budget")
		}
		// Rejected ticks did not create delayed work after this recipe finishes.
		if len(e.slot) != 0 {
			t.Fatal("cleaned recipe retained slot")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := testEngine("success").execute(ctx, recipeBytes(1, 3))
	if r.Outcome != Cancelled || len(r.Steps) != 0 {
		t.Fatal("cancelled caller launched")
	}
}

func awaitSlot(t *testing.T, e *engine) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(e.slot) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(e.slot) == 0 {
		t.Fatal("recipe did not acquire slot")
	}
}

func TestKillPrecedesReapingAndPendingCleanupHoldsSlot(t *testing.T) {
	for _, blockedWait := range []bool{true, false} {
		e := testEngine("success")
		var killed, waitStarted, launched atomic.Int32
		var allowAbsent atomic.Bool
		gate := make(chan struct{})
		realKill, realWait, realSupervisor, realAbsent := e.kill, e.wait, e.supervisor, e.groupAbsent
		e.kill = func(pid int) error {
			if pid <= 1 || waitStarted.Load() != 0 {
				t.Error("destructive signal after reaping began")
			}
			killed.Add(1)
			return realKill(pid)
		}
		e.wait = func(cmd *exec.Cmd) error {
			if killed.Load() != 1 {
				t.Error("wait before owned group kill")
			}
			waitStarted.Add(1)
			if blockedWait {
				<-gate
			}
			return realWait(cmd)
		}
		e.supervisor = func() *exec.Cmd { launched.Add(1); return realSupervisor() }
		e.groupAbsent = func(pid int) bool { return allowAbsent.Load() && realAbsent(pid) }
		r := e.execute(context.Background(), recipeBytes(2, 3))
		if r.Outcome != CleanupPending || len(r.Steps) != 1 || r.Steps[0].CleanupComplete {
			t.Fatalf("pending=%+v", r)
		}
		for i := 0; i < 20; i++ {
			if r := e.execute(context.Background(), recipeBytes(1, 3)); r.Outcome != RunnerBusy {
				t.Fatal("cleanup accumulated replacements")
			}
		}
		if killed.Load() != 1 || launched.Load() != 1 {
			t.Fatal("more than one owned group")
		}
		allowAbsent.Store(true)
		close(gate)
		limit := time.Now().Add(time.Second)
		for len(e.slot) != 0 && time.Now().Before(limit) {
			time.Sleep(time.Millisecond)
		}
		if len(e.slot) != 0 || killed.Load() != 1 {
			t.Fatal("slot recovery signalled after reaping or failed to release")
		}
	}
}
