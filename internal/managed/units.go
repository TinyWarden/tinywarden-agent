package managed

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func command(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	c.Dir = "/"
	if c.Run() != nil {
		return errors.New("system command failed: " + filepath.Base(name))
	}
	return nil
}
func system(ctx context.Context, args ...string) error {
	return command(ctx, "/usr/bin/systemctl", args...)
}
func unitQuote(base string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%").Replace(base)
}

func verifyUnits(ctx context.Context, base, release string) error {
	dir, e := os.MkdirTemp(filepath.Join(base, "updater", "private"), "verify-units-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	args := []string{"verify"}
	for _, name := range []string{Service, UpdateService, Timer} {
		input, e := os.ReadFile(filepath.Join(release, "systemd", name))
		if e != nil || len(input) > 16384 {
			return errors.New("invalid service template")
		}
		output := strings.ReplaceAll(string(input), "@BASE@", unitQuote(base))
		// Validate against the staged executable before changing the active pointer.
		output = strings.ReplaceAll(output, unitQuote(filepath.Join(base, "current", "tinywarden-agent")), unitQuote(filepath.Join(release, "tinywarden-agent")))
		path := filepath.Join(dir, name)
		if e = os.WriteFile(path, []byte(output), 0600); e != nil {
			return e
		}
		args = append(args, path)
	}
	return command(ctx, "/usr/bin/systemd-analyze", args...)
}
func units(ctx context.Context, base, release string) error {
	destination := filepath.Join(base, "updater", "systemd")
	if e := rootDirectory(destination, 0755); e != nil {
		return e
	}
	for _, name := range []string{Service, UpdateService, Timer} {
		input, e := os.ReadFile(filepath.Join(release, "systemd", name))
		if e != nil || len(input) > 16384 {
			return errors.New("invalid service template")
		}
		output := strings.ReplaceAll(string(input), "@BASE@", unitQuote(base))
		path := filepath.Join(destination, name)
		if e = os.WriteFile(path, []byte(output), 0644); e != nil {
			return e
		}
		link := filepath.Join("/etc/systemd/system", name)
		if i, e := os.Lstat(link); e == nil {
			target, _ := os.Readlink(link)
			if i.Mode()&os.ModeSymlink == 0 || target != path {
				return errors.New("conflicting systemd registration: " + link)
			}
		} else if !os.IsNotExist(e) {
			return e
		} else if e = os.Symlink(path, link); e != nil {
			return e
		}
	}
	return system(ctx, "daemon-reload")
}
func switchCurrent(base, version string) error {
	if !validTarget(version) {
		return errors.New("invalid release target")
	}
	path := filepath.Join(base, ".current-next")
	_ = os.Remove(path)
	if e := os.Symlink(filepath.Join("releases", version), path); e != nil {
		return e
	}
	if e := os.Rename(path, filepath.Join(base, "current")); e != nil {
		return e
	}
	return syncDirectory(base)
}
