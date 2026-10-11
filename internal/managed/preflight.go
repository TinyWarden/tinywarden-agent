package managed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func Preflight(base string, fresh bool) error {
	if os.Geteuid() != 0 {
		return errors.New("installation/update requires root; rerun with sudo")
	}
	if err := basePath(base); err != nil {
		return err
	}
	if fresh {
		if err := traversableParents(base); err != nil {
			return err
		}
		entries, err := os.ReadDir(base)
		if err == nil && len(entries) > 0 {
			return errors.New("installation directory is not empty; use explicit adoption or the installed update command")
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot read installation directory %s", base)
		}
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil || !hasRelease(string(osRelease), "ID", "debian") || !hasRelease(string(osRelease), "VERSION_ID", "13") || runtime.GOARCH != "amd64" {
		return errors.New("supported platform is Debian 13 x64 (amd64)")
	}
	missing := []string{}
	for _, path := range []string{"/usr/bin/python3.13", "/usr/bin/bwrap", "/usr/bin/openssl", "/usr/bin/systemctl", "/usr/bin/getent", "/usr/sbin/useradd", "/usr/sbin/runuser"} {
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing prerequisites: %s; install the corresponding Debian packages: python3.13 bubblewrap openssl systemd passwd util-linux", strings.Join(missing, ", "))
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return errors.New("cgroup v2 is required; enable it before installation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/python3.13", "-I", "-S", "-B", "-c", "import ctypes; ctypes.CDLL('libseccomp.so.2')").Run(); err != nil {
		return errors.New("libseccomp2 is required; install Debian package libseccomp2")
	}
	if err := exec.CommandContext(ctx, "/usr/bin/systemctl", "is-system-running", "--quiet").Run(); err != nil {
		if _, err := os.Stat("/run/systemd/system"); err != nil {
			return errors.New("a running systemd system manager is required")
		}
	}
	parent := base
	for {
		if _, err := os.Stat(parent); err == nil {
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			return errors.New("installation parent unavailable")
		}
		parent = next
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(parent, &fs); err != nil {
		return errors.New("cannot inspect installation filesystem")
	}
	if fs.Flags&syscall.MS_NOEXEC != 0 || fs.Flags&syscall.MS_RDONLY != 0 {
		return errors.New("installation filesystem must be writable and allow program execution; select another location or correct its mount options")
	}
	if fs.Bavail*uint64(fs.Bsize) < 256<<20 {
		return errors.New("at least 256 MiB free is required for staging and updating")
	}
	return nil
}

func hasRelease(data, key, value string) bool {
	for _, line := range strings.Split(data, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == key && strings.Trim(v, "\"'") == value {
			return true
		}
	}
	return false
}

func traversableParents(base string) error {
	_, gid, _ := account()
	for path := base; path != "/"; path = filepath.Dir(path) {
		i, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return errors.New("cannot inspect installation ancestry")
		}
		group := int(i.Sys().(*syscall.Stat_t).Gid)
		if i.Mode().Perm()&0001 == 0 && (gid == 0 || group != gid || i.Mode().Perm()&0010 == 0) {
			return errors.New("agent account cannot traverse " + path + "; choose another location or grant directory access before installation")
		}
	}
	return nil
}
