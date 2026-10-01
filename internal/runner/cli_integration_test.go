package runner

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// Build into a disposable directory, never over the installed/serving agent.
// This covers the real fixed CLI dispatch and descriptor numbering, not only
// the synthetic supervisor entry used for hostile-process cases.
func TestProductionCLISupervisor(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tinywarden-agent")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/tinywarden-agent")
	if build.Run() != nil {
		t.Fatal("disposable agent executable build failed")
	}
	e := newEngine()
	e.supervisor = func() *exec.Cmd { return exec.Command(binary, SupervisorArgument) }
	r := e.execute(context.Background(), recipeBytes(1, 3))
	if r.Outcome != Exited || len(r.Steps) != 1 || !r.Steps[0].CleanupComplete ||
		r.Steps[0].ExitCode == nil || (*r.Steps[0].ExitCode != 0 && *r.Steps[0].ExitCode != 1) ||
		len(r.Steps[0].Stdout) != 0 || len(r.Steps[0].Stderr) != 0 {
		t.Fatalf("real CLI supervisor: outcome=%s", r.Outcome)
	}
}
