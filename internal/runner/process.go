package runner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type stepPipes struct {
	requestRead, requestWrite *os.File
	outRead, outWrite         *os.File
	errRead, errWrite         *os.File
	controlRead, controlWrite *os.File
}

func openPipes() (*stepPipes, error) {
	p := &stepPipes{}
	for _, pair := range [][2]**os.File{{&p.requestRead, &p.requestWrite}, {&p.outRead, &p.outWrite}, {&p.errRead, &p.errWrite}, {&p.controlRead, &p.controlWrite}} {
		r, w, err := os.Pipe()
		if err != nil {
			p.close()
			return nil, err
		}
		*pair[0], *pair[1] = r, w
	}
	return p, nil
}

func (p *stepPipes) close() {
	for _, f := range []*os.File{p.requestRead, p.requestWrite, p.outRead, p.outWrite, p.errRead, p.errWrite, p.controlRead, p.controlWrite} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func (e *engine) runStep(ctx context.Context, step Step, deadline time.Time) (result StepResult, held bool) {
	start := time.Now()
	defer func() { result.Duration = time.Since(start) }()
	result = StepResult{ID: step.ID, Profile: step.Profile, Outcome: SpawnFailed, CleanupComplete: true}
	p, err := openPipes()
	if err != nil {
		return result, false
	}
	defer p.close()
	budget := time.Until(deadline)
	if d, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(d))
	}
	if budget <= 0 || ctx.Err() != nil {
		result.Outcome = interruption(ctx)
		return result, false
	}
	cmd := e.supervisor()
	cmd.Dir, cmd.Env = "/", cleanEnvironment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = p.requestRead, p.controlWrite, nil
	cmd.ExtraFiles = []*os.File{p.outWrite, p.errWrite}
	if sealDescriptors() != nil {
		result.Outcome = PolicyRejected
		return result, false
	}
	if cmd.Start() != nil {
		return result, false
	}
	_ = p.requestRead.Close()
	_ = p.controlWrite.Close()
	_ = p.outWrite.Close()
	_ = p.errWrite.Close()
	out, stderr, control := captureStream(p.outRead, StdoutLimit), captureStream(p.errRead, StderrLimit), captureControl(p.controlRead)
	data, _ := json.Marshal(request{Step: step, Budget: int64(budget)})
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	result.Outcome = HelperFailed
	if _, err = p.requestWrite.Write(frame); err == nil {
		select {
		case c := <-control.result:
			if c.valid {
				result.Outcome, result.ExitCode, result.Signal = c.completion.Outcome, c.completion.ExitCode, c.completion.Signal
			}
		case <-out.overflow:
			result.Outcome = OutputLimit
		case <-stderr.overflow:
			result.Outcome = OutputLimit
		case <-ctx.Done():
			result.Outcome = interruption(ctx)
		}
	}
	// The leader is still our unreaped child here, even if it died unexpectedly.
	// This is the only destructive group signal; Wait is started strictly after it.
	_ = e.kill(cmd.Process.Pid)
	_ = p.requestWrite.Close()
	held = e.finishStep(ctx, cmd, p, out, stderr, control, &result)
	return result, held
}

func (e *engine) finishStep(ctx context.Context, cmd *exec.Cmd, p *stepPipes,
	out, stderr capture, control controlCapture, result *StepResult) bool {
	reaped := make(chan struct{})
	go func() { _ = e.wait(cmd); close(reaped) }()
	cleanupDeadline := time.Now().Add(time.Second)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	var stdoutResult, stderrResult streamResult
	outDone, errDone, controlDone, waitDone := out.done, stderr.done, control.done, (<-chan struct{})(reaped)
	complete := false
	for !complete {
		select {
		case stdoutResult = <-outDone:
			outDone = nil
		case stderrResult = <-errDone:
			errDone = nil
		case clean := <-controlDone:
			if !clean {
				result.Outcome = HelperFailed
			}
			controlDone = nil
		case <-waitDone:
			waitDone = nil
		case <-timer.C:
			// Closing our readers unblocks read goroutines without waiting for an
			// escaped/stuck descriptor holder. Preserve only their final values.
			_ = p.outRead.Close()
			_ = p.errRead.Close()
			_ = p.controlRead.Close()
			if outDone != nil {
				stdoutResult = <-outDone
				outDone = nil
			}
			if errDone != nil {
				stderrResult = <-errDone
				errDone = nil
			}
			if controlDone != nil {
				<-controlDone
				controlDone = nil
			}
			complete = true
		}
		if outDone == nil && errDone == nil && controlDone == nil && waitDone == nil {
			complete = true
		}
	}
	result.Stdout, result.Stderr = stdoutResult.data, stderrResult.data
	result.StdoutTruncated, result.StderrTruncated = stdoutResult.truncated, stderrResult.truncated
	// Full stream evidence owns the final result, even when control won the
	// initial select. Deadline/cancellation also wins until all evidence is in.
	if interrupted(ctx) {
		result.Outcome = interruption(ctx)
	}
	if stdoutResult.truncated || stderrResult.truncated {
		result.Outcome = OutputLimit
	}
	if (stdoutResult.failed || stderrResult.failed) && result.Outcome == Exited {
		result.Outcome = HelperFailed
	}
	if result.Outcome == HelperFailed {
		result.ExitCode, result.Signal = nil, nil
	}
	// Descendants orphaned by the group kill may need a moment to be reaped by
	// init. Use the remaining cleanup window before reporting cleanup pending.
	for time.Now().Before(cleanupDeadline) {
		select {
		case <-reaped:
			if e.groupAbsent(cmd.Process.Pid) {
				result.CleanupComplete = true
				return false
			}
		default:
		}
		time.Sleep(min(5*time.Millisecond, time.Until(cleanupDeadline)))
	}
	select {
	case <-reaped:
		if e.groupAbsent(cmd.Process.Pid) {
			result.CleanupComplete = true
			return false
		}
	default:
	}
	result.CleanupComplete = false
	result.Outcome = CleanupPending
	// At most one observer/reaper exists because this engine's slot stays held.
	// After reaping, probing is read-only: never kill a possibly reused group ID.
	go func() {
		<-reaped
		for !e.groupAbsent(cmd.Process.Pid) {
			time.Sleep(250 * time.Millisecond)
		}
		<-e.slot
	}()
	return true
}
