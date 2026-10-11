package managed

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/platform"
)

const DefaultBase = "/opt/tinywarden-agent"
const Service = "tinywarden-agent.service"
const UpdateService = "tinywarden-agent-update.service"
const Timer = "tinywarden-agent-update.timer"

func safePath(path string, allowMissing bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n\t\\\"") {
		return errors.New("installation path must be a clean absolute path")
	}
	current := "/"
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) && allowMissing {
			return nil
		}
		if err != nil {
			return fmt.Errorf("cannot inspect %s", current)
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		// Linux POSIX ACL mask write permission is reflected in group mode bits.
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || s.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("unsafe installation parent %s: use a root-owned directory without group/other write permission", current)
		}
	}
	return nil
}

func basePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("choose a dedicated absolute installation directory")
	}
	for _, reserved := range []string{"/opt", "/srv", "/usr", "/usr/local", "/var", "/var/lib", "/etc", "/bin", "/sbin", "/lib", "/root", "/home", "/dev", "/proc", "/sys", "/run"} {
		if path == reserved || strings.HasPrefix(path, "/proc/") || strings.HasPrefix(path, "/sys/") || strings.HasPrefix(path, "/dev/") || strings.HasPrefix(path, "/run/") {
			return errors.New("choose a dedicated persistent installation directory")
		}
	}
	return safePath(path, true)
}

func atomicFile(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func lock(base string) (*os.File, error) {
	path := filepath.Join(base, "updater", "private", "lock")
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Sys().(*syscall.Stat_t).Uid != 0 || i.Mode().Perm() != 0600 {
		f.Close()
		return nil, errors.New("unsafe update lock")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another installation or update is in progress")
	}
	return f, nil
}

func status(base string, s platform.UpdateStatus) error {
	return atomicFile(filepath.Join(base, "updater", "status.json"), s, 0644)
}
func failStatus(base, target, code string) error {
	s := platform.UpdateStatus{Outcome: "failed", Target: target, Error: code}
	return status(base, s)
}

func rootDirectory(path string, mode os.FileMode) error {
	if _, e := os.Lstat(path); e == nil {
		return safePath(path, false)
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := rootDirectory(filepath.Dir(path), 0755); e != nil {
		return e
	}
	if e := os.Mkdir(path, mode); e != nil {
		return e
	}
	// Caller umask must not silently make public release paths unreadable to monitoring.
	if e := os.Chmod(path, mode); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
