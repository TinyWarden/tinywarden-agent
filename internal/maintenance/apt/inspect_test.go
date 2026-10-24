package apt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

func TestInspectorNativeLocksSurviveCallerCancellationDuringCommit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on designated disposable Debian host")
	}
	if len(Prerequisites()) != 0 {
		t.Fatal("missing native maintenance prerequisites")
	}
	base := t.TempDir()
	if e := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); e != nil {
		t.Fatal(e)
	}
	guard, e := hostmutation.Lock(base)
	if e != nil {
		t.Fatal(e)
	}
	defer guard.Close()
	provider := &Provider{Base: base, Guard: guard, Action: "packages.upgrade_conservative.v1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	e = provider.Inspect(ctx, func(facts map[string]string) error {
		called = true
		cancel()
		if len(facts) != 2 || len(facts["state_sha256"]) != 64 || len(facts["selection_sha256"]) != 64 {
			t.Fatal("missing integrity proof")
		}
		cmd := exec.Command("/usr/bin/python3", "-I", "-B", "-c", `import fcntl,os
f=os.open('/var/lib/dpkg/lock-frontend',os.O_RDWR)
try:
 fcntl.lockf(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:
 raise SystemExit(0)
raise SystemExit(19)`)
		if e := cmd.Run(); e != nil {
			t.Fatal("native lock was not held throughout local commit", e)
		}
		return nil
	})
	if e != nil || !called {
		t.Fatal("read-only native inspection failed", e)
	}
}
