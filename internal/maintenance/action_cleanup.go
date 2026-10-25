package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

// cleanupArchivedAction releases staging only after its acknowledged journal is
// durably archived. Immutable apply/core receipts remain as replay evidence.
// Callers hold the common mutation guard and exclude every managed worker.
func cleanupArchivedAction(base string, j Journal) error {
	if j.Validate() != nil || !j.Acknowledged || j.Phase != "terminal" || j.Attention || j.Result == nil || j.Result.ProcessRunning && j.Recovery == nil {
		return errors.New("maintenance_reconciliation_required")
	}
	if pending, e := PendingRecovery(base); e != nil || pending {
		return errors.New("maintenance_recovery_delivery_pending")
	}
	if fence, e := hostmutation.ReadFence(base); e != nil || fence != nil {
		return errors.New("maintenance_reconciliation_required")
	}
	private := filepath.Join(base, "updater", "private")
	for _, path := range []string{private, filepath.Join(private, "action-packages")} {
		info, e := os.Lstat(path)
		if os.IsNotExist(e) && path != private {
			continue
		}
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return errors.New("action_cache_invalid")
		}
	}

	if e := os.RemoveAll(filepath.Join(private, "action-state-"+j.Intent.ID)); e != nil {
		return e
	}
	for _, phase := range []string{"prepare", "fetch", "verify", "inspect"} {
		path, e := ArtifactPath(base, j.Intent.ID, "action_"+phase)
		if e != nil {
			return e
		}
		if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if j.Intent.Binding != nil {
		store := filepath.Join(private, "action-packages")
		if e := os.RemoveAll(filepath.Join(store, j.Intent.Binding.ContentSHA)); e != nil {
			return e
		}
		if e := os.Remove(filepath.Join(store, "."+j.Intent.Binding.ContentSHA+".metadata.json")); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	directory, e := os.Open(private)
	if e != nil {
		return e
	}
	defer directory.Close()
	return directory.Sync()
}
