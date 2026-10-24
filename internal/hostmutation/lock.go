// Package hostmutation serializes native maintenance and signed installation changes.
package hostmutation

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var ErrBusy = errors.New("another installation or update is in progress")

// Lock preserves the deployed updater lock path for older installed executables.
// The caller verifies the managed installation before acquiring this guard.
func Lock(base string) (*os.File, error) {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return nil, errors.New("unsafe update lock")
	}
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
		return nil, ErrBusy
	}
	return f, nil
}
