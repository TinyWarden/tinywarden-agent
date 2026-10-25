package installation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionAgentHasNoEmbeddedDomainExecutor(t *testing.T) {
	root := filepath.Clean("../..")
	cmd := exec.Command("go", "list", "-deps", "./cmd/tinywarden-agent")
	cmd.Dir = root
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(string(raw), e)
	}
	if strings.Contains(string(raw), "/maintenance/apt") {
		t.Fatal("retired APT provider linked into production agent")
	}
	if _, e = os.Stat(filepath.Join(root, "internal", "maintenance", "apt")); !os.IsNotExist(e) {
		t.Fatal("retired executor retained in active source")
	}
	for _, name := range []string{"actions/runner.py", "actions/guest.py", "actions/guardian.py", "actions/probe.py", "actions/readiness.py"} {
		raw, e = os.ReadFile(filepath.Join(root, "sdk", "python", "runtime", name))
		if e != nil {
			t.Fatal(e)
		}
		for _, forbidden := range []string{"import apt", "apt_pkg", "packages.upgrade_conservative", "packages.refresh.v1"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("domain executor in generic runtime %s", name)
			}
		}
	}
}
