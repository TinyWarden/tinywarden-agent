package hostmutation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsUntrustedLock(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "updater", "private")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "unrelated"), filepath.Join(dir, "lock")); err != nil {
		t.Fatal(err)
	}
	if guard, err := Lock(base); err == nil {
		guard.Close()
		t.Fatal("followed a symlink")
	}
	if _, err := Lock("relative"); err == nil {
		t.Fatal("accepted a relative managed path")
	}
}

func TestNativeMutationsShareDeployedGuard(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on the designated disposable host")
	}
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	one, err := Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	if two, err := Lock(base); err == nil {
		two.Close()
		t.Fatal("overlapping native mutation")
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	two, err := Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	two.Close()
}
