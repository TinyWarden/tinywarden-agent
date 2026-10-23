package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These dispatches exist only in the Go test executable. Production has no
// fixture profile, environment flag or alternate executable-path setting.
// Direct syscall.Exit avoids the race runtime exit grace period changing the
// synthetic command deadlines; the parent ownership code remains race-tested.
func fixtureCommand(args ...string) *exec.Cmd {
	return exec.Command(os.Args[0], append([]string{"-test.run=^TestProcessFixture$", "--"}, args...)...)
}

func testEngine(mode string) *engine {
	e := newEngine()
	e.supervisor = func() *exec.Cmd { return fixtureCommand("supervisor", mode) }
	return e
}

func recipeBytes(steps int, seconds int) []byte {
	r := Recipe{1, Capability, 1, seconds, nil}
	for i := 0; i < steps; i++ {
		r.Steps = append(r.Steps, Step{fmt.Sprintf("s%d", i), "reboot-marker.v1", []string{"-e", "/run/reboot-required"}})
	}
	data, _ := json.Marshal(r)
	return data
}

func TestProcessFixture(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i + 1
			break
		}
	}
	if index < 0 {
		return
	}
	args := os.Args[index:]
	if args[0] == "supervisor" {
		mode := args[1]
		switch mode {
		case "early":
			syscall.Exit(17)
		case "malformed":
			fmt.Println(`{"outcome":"exited","exit_code":"secret","signal":null}`)
			syscall.Exit(0)
		case "control-flood":
			fmt.Print(strings.Repeat("x", controlLimit+1))
			syscall.Exit(0)
		case "forged":
			fmt.Print("{\"outcome\":\"exited\",\"exit_code\":0,\"signal\":null}\nextra")
			syscall.Exit(0)
		}
		prepare := prepareCommand
		command := exec.Command
		if mode != "native" {
			command = func(_ string, _ ...string) *exec.Cmd {
				if mode == "spawn" {
					return exec.Command("/nonexistent/tinywarden-synthetic-tool")
				}
				return fixtureCommand("command", mode)
			}
		}
		if mode == "policy" {
			prepare = func(string) error { return errPolicy }
		}
		syscall.Exit(supervise(os.Stdin, os.Stdout, os.NewFile(3, "out"), os.NewFile(4, "err"), prepare, command))
	}
	if args[0] == "parent" {
		testEngine("tree:"+args[1]).execute(context.Background(), recipeBytes(1, 30))
		syscall.Exit(1)
	}
	if args[0] != "command" {
		syscall.Exit(91)
	}
	mode, file, _ := strings.Cut(args[1], ":")
	switch mode {
	case "success":
		fmt.Fprint(os.Stdout, "out")
		fmt.Fprint(os.Stderr, "err")
	case "nonzero":
		syscall.Exit(7)
	case "signal":
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
	case "sleep":
		fmt.Fprintln(os.Stdout, os.Getpid())
		time.Sleep(10 * time.Second)
	case "brief":
		time.Sleep(600 * time.Millisecond)
	case "tree", "orphan", "tree-flood":
		cmd := fixtureCommand("command", "sleep")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if cmd.Start() != nil {
			syscall.Exit(92)
		}
		line := fmt.Sprintf("%d %d %d\n", os.Getpid(), cmd.Process.Pid, syscall.Getpgrp())
		fmt.Fprint(os.Stdout, line)
		if file != "" {
			_ = os.WriteFile(file, []byte(line), 0600)
		}
		if mode == "tree" {
			_ = cmd.Wait()
		}
		if mode == "tree-flood" {
			for {
				_, _ = os.Stdout.Write([]byte(strings.Repeat("f", 4096)))
			}
		}
	case "out-exact", "out-over", "err-exact", "err-over", "both-exact":
		count, writer := StdoutLimit, io.Writer(os.Stdout)
		if strings.HasPrefix(mode, "err") {
			count, writer = StderrLimit, os.Stderr
		}
		if strings.HasSuffix(mode, "over") {
			count++
		}
		_, _ = writer.Write([]byte(strings.Repeat("x", count)))
		if mode == "both-exact" {
			_, _ = os.Stderr.Write([]byte(strings.Repeat("e", StderrLimit)))
		}
	case "flood":
		for {
			_, _ = os.Stdout.Write([]byte(strings.Repeat("f", 4096)))
		}
	case "inspect":
		inspectCommand(file)
	case "raw":
		fmt.Fprint(os.Stdout, "synthetic-secret\n{\"outcome\":\"exited\",\"exit_code\":0}")
	default:
		syscall.Exit(93)
	}
	syscall.Exit(0)
}

type inspection struct {
	Environment      []string
	Directory        string
	NullInput        bool
	NoNewPrivileges  bool
	LeakedDescriptor bool
}

func inspectCommand(secretFile string) {
	cwd, _ := os.Getwd()
	data, _ := io.ReadAll(os.Stdin)
	status, _ := os.ReadFile("/proc/self/status")
	info := inspection{Environment: os.Environ(), Directory: cwd, NullInput: len(data) == 0,
		NoNewPrivileges: strings.Contains(string(status), "NoNewPrivs:\t1")}
	entries, _ := os.ReadDir("/proc/self/fd")
	for _, entry := range entries {
		n, _ := strconv.Atoi(entry.Name())
		link, _ := os.Readlink("/proc/self/fd/" + entry.Name())
		if n > 2 && link == secretFile {
			info.LeakedDescriptor = true
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(info)
}
