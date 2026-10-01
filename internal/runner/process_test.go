package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func ownedPIDs(t *testing.T, data []byte) []int {
	t.Helper()
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		t.Fatal("missing child/grandchild/group ownership")
	}
	pids := make([]int, 3)
	for i := range pids {
		pid, err := strconv.Atoi(fields[i])
		if err != nil || pid <= 1 {
			t.Fatal("invalid fixture identity")
		}
		pids[i] = pid
	}
	return pids
}

func awaitAbsent(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, pid := range pids {
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				all = false
			}
		}
		if all {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) != syscall.ESRCH {
			t.Errorf("owned fixture %d survived", pid)
		}
	}
}

func TestDescendantsAndUnrelatedSentinel(t *testing.T) {
	sentinel := fixtureCommand("command", "sleep")
	sentinel.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sentinel.Process.Kill(); _ = sentinel.Wait() }()
	for _, mode := range []string{"tree", "orphan", "tree-flood", "early", "malformed", "control-flood"} {
		r := testEngine(mode).execute(context.Background(), recipeBytes(1, 1))
		wanted := TimedOut
		if mode == "orphan" {
			wanted = Exited
		}
		if mode == "tree-flood" {
			wanted = OutputLimit
		}
		if mode == "early" || mode == "malformed" || mode == "control-flood" {
			wanted = HelperFailed
		}
		if r.Outcome != wanted || !r.Steps[0].CleanupComplete {
			t.Fatalf("%s: outcome=%s", mode, r.Outcome)
		}
		if mode == "tree" || mode == "orphan" || mode == "tree-flood" {
			awaitAbsent(t, ownedPIDs(t, r.Steps[0].Stdout))
		}
		if syscall.Kill(sentinel.Process.Pid, 0) != nil {
			t.Fatal("unrelated sentinel was killed")
		}
	}
}

func TestParentDeathKillsSupervisorAndDescendants(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owned-processes")
	parent := fixtureCommand("parent", file)
	parent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if parent.Start() != nil {
		t.Fatal("parent fixture failed")
	}
	defer func() {
		if parent.ProcessState == nil {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
	}()
	var data []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(file)
		if len(data) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	pids := ownedPIDs(t, data)
	if parent.Process.Kill() != nil {
		t.Fatal("could not stop owned parent")
	}
	_ = parent.Wait()
	// The recipe's 30-second budget has not elapsed; private-pipe EOF must do
	// this cleanup without the vanished parent's signal or scheduling loop.
	awaitAbsent(t, pids)
}

func TestRecipeUsesOneWholeDeadline(t *testing.T) {
	start := time.Now()
	r := testEngine("brief").execute(context.Background(), recipeBytes(3, 1))
	if r.Outcome != TimedOut || len(r.Steps) != 2 || r.Steps[0].ExitCode == nil || *r.Steps[0].ExitCode != 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("whole deadline not enforced: %s steps=%d", r.Outcome, len(r.Steps))
	}
}
