package hostmutation

import (
	"encoding/json"
	"errors"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// Fence survives process/lease loss. Only the maintenance owner reconciles it.
type Fence struct {
	Version    int    `json:"schema_version"`
	JobID      string `json:"job_id"`
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
}

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (f Fence) Valid() bool {
	return f.Version == 1 && identifier.MatchString(f.JobID) && identifier.MatchString(f.HostID) && identifier.MatchString(f.AgentID) && f.Generation > 0 && f.Generation <= 9007199254740991
}
func fencePath(base string) string {
	return filepath.Join(base, "updater", "private", "maintenance-active.json")
}

// ReadFence requires the caller to hold Lock and to validate the managed base.
func ReadFence(base string) (*Fence, error) {
	fd, err := syscall.Open(fencePath(base), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("maintenance_reconciliation_required")
	}
	file := os.NewFile(uintptr(fd), fencePath(base))
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Size() > 2048 {
		return nil, errors.New("maintenance_reconciliation_required")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 2049))
	var fence Fence
	if err != nil || len(raw) > 2048 || agentrelease.Decode(raw, &fence) != nil || !fence.Valid() {
		return nil, errors.New("maintenance_reconciliation_required")
	}
	return &fence, nil
}
func AssertIdle(base string) error {
	f, err := ReadFence(base)
	if err != nil {
		return err
	}
	if f != nil {
		return errors.New("maintenance_reconciliation_required")
	}
	return nil
}

// WriteFence is atomic and durable; existing or corrupt scope cannot be replaced.
func WriteFence(base string, fence Fence) error {
	if !fence.Valid() || os.Geteuid() != 0 {
		return errors.New("maintenance_scope_invalid")
	}
	old, err := ReadFence(base)
	if err != nil {
		return err
	}
	if old != nil {
		if *old == fence {
			return nil
		}
		return errors.New("maintenance_busy")
	}
	raw, err := json.Marshal(fence)
	if err != nil {
		return err
	}
	return PrivateWrite(fencePath(base), raw)
}

// ClearFence is allowed only after durable reconciliation/terminal acknowledgement.
func ClearFence(base string, expected Fence) error {
	old, err := ReadFence(base)
	if err != nil {
		return err
	}
	if old == nil {
		return nil
	}
	if *old != expected {
		return errors.New("maintenance_scope_invalid")
	}
	if err = os.Remove(fencePath(base)); err != nil {
		return err
	}
	return syncParent(fencePath(base))
}
func syncParent(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// PrivateWrite is for root-owned worker records in an already validated private directory.
func PrivateWrite(path string, raw []byte) error {
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != 0 || os.Geteuid() != 0 {
		return errors.New("maintenance_journal_invalid")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".maintenance-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncParent(path)
}
