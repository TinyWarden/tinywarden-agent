package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skills/runtime"
)

type cappedWriter struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.max {
		w.overflow = true
		return 0, errors.New("collector output overflow")
	}
	return w.Buffer.Write(p)
}

var collectorSlot = make(chan struct{}, 1)

// A timed-out syscall may remain uninterruptible. Its slot stays held until Wait reaps it.
func runCollector(ctx context.Context) CollectorResult {
	executable, err := os.Executable()
	if err != nil {
		return failedCollection("collector_failed")
	}
	return runCollectorCommand(ctx, exec.Command(executable, "__collect-disk-v1"), 10*time.Second)
}

func failedCollection(reason string) CollectorResult {
	return CollectorResult{Coverage: "incomplete", Reason: reason, Mounts: []DiskMount{}}
}

// Production calls this only with the current executable and fixed helper argument.
func runCollectorCommand(ctx context.Context, cmd *exec.Cmd, budget time.Duration) CollectorResult {
	release, acquired := skillruntime.TryAcquire()
	if !acquired {
		return failedCollection("collector_timeout")
	}
	select {
	case collectorSlot <- struct{}{}:
	default:
		release()
		return failedCollection("collector_timeout")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := &cappedWriter{max: maxCollectorOutput}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		<-collectorSlot
		release()
		return failedCollection("collector_failed")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); <-collectorSlot; release() }()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case err := <-done:
		if out.overflow || out.Len() >= maxCollectorOutput {
			return failedCollection("output_overflow")
		}
		if err != nil {
			return failedCollection("collector_failed")
		}
		var result CollectorResult
		if json.Unmarshal(out.Bytes(), &result) != nil {
			return failedCollection("collector_failed")
		}
		return result
	case <-timer.C:
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return failedCollection("collector_timeout")
	case <-ctx.Done():
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return failedCollection("collector_failed")
	}
}
