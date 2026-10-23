package runner

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// Supervise is the fixed internal CLI entry. Descriptors 0/1 carry only the
// private request/control protocol; 3/4 are the parent's command output pipes.
// It never reads credentials, configuration, agent state or the environment.
func Supervise() int {
	return supervise(os.Stdin, os.Stdout, os.NewFile(3, "observation-stdout"),
		os.NewFile(4, "observation-stderr"), prepareCommand, exec.Command)
}

// The function parameters permit synthetic process fixtures in package tests;
// production dispatch always uses the fixed policy checker and exec.Command.
func supervise(input *os.File, control io.Writer, stdout, stderr *os.File,
	prepare func(string) error, command func(string, ...string) *exec.Cmd) int {
	pid := os.Getpid()
	pgid, err := syscall.Getpgid(pid)
	if err != nil || pid <= 1 || pgid != pid {
		return 1 // Never kill a caller's terminal/service group.
	}
	var frame [4]byte
	if _, err := io.ReadFull(input, frame[:]); err != nil {
		return 1
	}
	length := binary.BigEndian.Uint32(frame[:])
	if length == 0 || length > MaxRecipeBytes {
		return 1
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(input, data); err != nil {
		return 1
	}
	var req request
	if decodeStrict(data, &req) != nil || req.Budget < 1 || req.Budget > int64(30*time.Second) {
		return 1
	}
	path, err := executable(req.Step)
	if err != nil {
		return 1
	}
	// EOF means the parent is gone; any byte after the frame is invalid. Neither
	// this reader nor the watchdog depends on the direct command finishing.
	killOwnGroup := func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }
	go func() {
		var extra [1]byte
		_, _ = input.Read(extra[:])
		killOwnGroup()
	}()
	watchdog := time.AfterFunc(time.Duration(req.Budget), killOwnGroup)
	defer watchdog.Stop()
	runtime.LockOSThread()
	// no_new_privs is irreversible for this thread. It belongs to a short-lived
	// supervisor; never unlock it for unrelated Go work after setting the flag.
	if prepare(path) != nil {
		return reportAndHold(control, completion{Outcome: PolicyRejected})
	}
	stdin, err := os.Open("/dev/null")
	if err != nil {
		return reportAndHold(control, completion{Outcome: SpawnFailed})
	}
	defer stdin.Close()
	cmd := command(path, req.Step.Argv...)
	cmd.Dir = "/"
	cmd.Env = cleanEnvironment()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	// No ExtraFiles: request/control descriptors must not reach the command.
	if cmd.Start() != nil {
		return reportAndHold(control, completion{Outcome: SpawnFailed})
	}
	err = cmd.Wait()
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || (err != nil && !status.Exited() && !status.Signaled()) {
		return 1
	}
	result := completion{Outcome: Exited}
	if status.Signaled() {
		value := int(status.Signal())
		result.Signal = &value
	} else {
		value := status.ExitStatus()
		result.ExitCode = &value
	}
	return reportAndHold(control, result)
}

func reportAndHold(control io.Writer, result completion) int {
	if json.NewEncoder(control).Encode(result) != nil {
		return 1
	}
	// Keeping the leader alive anchors its PID until the parent kills the group
	// and reaps it. The parent-death reader and watchdog remain active.
	select {}
}
