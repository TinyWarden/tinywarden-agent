package runner

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startFramedFixture(t *testing.T, mode string) (*exec.Cmd, *stepPipes) {
	t.Helper()
	p, err := openPipes()
	if err != nil {
		t.Fatal(err)
	}
	cmd := fixtureCommand("supervisor", mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = p.requestRead, p.controlWrite, nil
	cmd.ExtraFiles = []*os.File{p.outWrite, p.errWrite}
	if cmd.Start() != nil {
		p.close()
		t.Fatal("fixture start failed")
	}
	_ = p.requestRead.Close()
	_ = p.outWrite.Close()
	_ = p.errWrite.Close()
	_ = p.controlWrite.Close()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
		p.close()
	})
	return cmd, p
}

func writeFrame(t *testing.T, p *stepPipes, budget time.Duration) {
	t.Helper()
	data, _ := json.Marshal(request{Step: Step{"s", "reboot-marker.v1", []string{"-e", "/run/reboot-required"}}, Budget: int64(budget)})
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	if _, err := p.requestWrite.Write(frame); err != nil {
		t.Fatal("frame write failed")
	}
}

func TestSupervisorWatchdogAndExtraFrameData(t *testing.T) {
	for _, extra := range []bool{false, true} {
		cmd, p := startFramedFixture(t, "sleep")
		budget := time.Duration(100) * time.Millisecond
		if extra {
			budget = 3 * time.Second
		}
		writeFrame(t, p, budget)
		data := make(chan []byte, 1)
		go func() { b, _ := io.ReadAll(p.outRead); data <- b }()
		if extra {
			_, _ = p.requestWrite.Write([]byte("invalid-extra"))
		}
		select {
		case b := <-data:
			if len(b) > 0 {
				pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err != nil {
					t.Fatal("bad PID evidence")
				}
				awaitAbsent(t, []int{pid})
			}
		case <-time.After(time.Second):
			t.Fatal("independent watchdog/protocol cleanup failed")
		}
		_ = cmd.Wait() // The supervisor killed its group before this reap.
		if syscall.Kill(-cmd.Process.Pid, 0) != syscall.ESRCH {
			t.Fatal("watchdog group remains")
		}
	}
}

func TestInvalidFramesCannotLaunchCommand(t *testing.T) {
	for _, frame := range [][]byte{{0, 0, 0, 0}, {0, 0, 32, 1}, {0, 0, 0, 20, '{'}, {0, 0, 0, 2, '{', '}'}} {
		cmd, p := startFramedFixture(t, "success")
		_, _ = p.requestWrite.Write(frame)
		_ = p.requestWrite.Close()
		data, err := io.ReadAll(p.outRead)
		if err != nil || len(data) != 0 {
			t.Fatal("invalid request launched command")
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}
}
