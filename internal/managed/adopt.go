package managed

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/agent"
	"github.com/TinyWarden/tinywarden-agent/internal/platform"
	sdk "github.com/TinyWarden/tinywarden-agent/internal/skills/runtime"
)

const legacyData = "/var/lib/tinywarden-agent"

func inspectLegacy() error {
	for _, path := range []string{"/etc/tinywarden-agent/agent.json", "/usr/local/bin/tinywarden-agent", "/etc/systemd/system/" + Service} {
		i, e := os.Lstat(path)
		if e != nil || !i.Mode().IsRegular() || i.Sys().(*syscall.Stat_t).Uid != 0 || i.Mode().Perm()&0022 != 0 {
			return errors.New("unknown legacy installation: inspect " + path)
		}
	}
	c, e := legacyConfig()
	if e != nil {
		return e
	}
	if c.StateDir != legacyData || c.RuntimeAssets != "" && c.RuntimeAssets != "/usr/local/lib/tinywarden-agent/runtime" || c.Managed || c.Updates != nil {
		return errors.New("custom legacy layout requires manual migration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5e9)
	defer cancel()
	b, e := exec.CommandContext(ctx, "/usr/local/bin/tinywarden-agent", "--version").Output()
	if e != nil || !strings.HasSuffix(strings.TrimSpace(string(b)), " 0.0.7") {
		return errors.New("adoption supports only the known 0.0.7 installation")
	}
	b, e = exec.CommandContext(ctx, "/usr/bin/systemctl", "show", Service, "--value", "-p", "DropInPaths").Output()
	if e != nil || len(bytes.TrimSpace(b)) != 0 {
		return errors.New("remove or manually migrate custom service overrides before adoption")
	}
	b, e = os.ReadFile("/etc/systemd/system/" + Service)
	if e != nil || !bytes.Contains(b, []byte("User=tinywarden-agent\n")) || !bytes.Contains(b, []byte("ExecStart=/usr/bin/env /usr/local/bin/tinywarden-agent run --config /etc/tinywarden-agent/agent.json\n")) {
		return errors.New("unknown legacy service entrypoint")
	}
	return nil
}
func legacyConfig() (agent.Config, error) {
	var c agent.Config
	b, e := platform.ReadPublic("/etc/tinywarden-agent/agent.json", 8192)
	if e != nil {
		return c, e
	}
	e = platform.Decode(b, &c)
	if e == nil {
		_, e = platform.Origin(c.ControlPlaneOrigin)
	}
	return c, e
}
func adopt(ctx context.Context, base string, automatic bool) (agent.Config, error) {
	c, e := legacyConfig()
	if e != nil {
		return c, e
	}
	uid, gid, e := account()
	if e != nil {
		return c, e
	}
	backup := filepath.Join(base, "updater", "private", "legacy")
	if e = os.Mkdir(backup, 0700); e != nil {
		return c, e
	}
	for _, path := range []string{"/etc/tinywarden-agent/agent.json", "/etc/systemd/system/" + Service, "/usr/local/bin/tinywarden-agent"} {
		b, e := os.ReadFile(path)
		if e != nil {
			return c, e
		}
		if e = os.WriteFile(filepath.Join(backup, filepath.Base(path)), b, 0600); e != nil {
			return c, e
		}
	}
	if e = system(ctx, "stop", Service); e != nil {
		return c, e
	}
	data := filepath.Join(base, "data")
	if e = os.Mkdir(data, 0700); e != nil {
		return c, e
	}
	if e = os.Chown(data, uid, gid); e != nil {
		return c, e
	}
	// Read agent-controlled files as the agent, never with root's filesystem access.
	release := filepath.Join(base, "releases", platform.Version, "tinywarden-agent")
	if e = command(ctx, "/usr/sbin/runuser", "-u", "tinywarden-agent", "--", release, "__copy-legacy", "--destination", data); e != nil {
		return c, e
	}
	c.Managed = true
	c.StateDir = filepath.Join(base, "data")
	c.RuntimeAssets = ""
	c.Updates = &platform.UpdateSettings{Automatic: automatic}
	if e = bind(base, &c); e != nil {
		return c, e
	}
	return c, nil
}

func CopyLegacy(destination string) error {
	if os.Geteuid() == 0 {
		return errors.New("state copying must run as the agent account")
	}
	i, e := os.Lstat(destination)
	if e != nil || !i.IsDir() || i.Mode().Perm() != 0700 || i.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe migration destination")
	}
	return copyTree(legacyData, destination, os.Geteuid(), os.Getegid())
}

func restartLegacyOnFailure(ctx context.Context) {
	// Only restart the untouched old registration; never roll managed state back.
	if inspectLegacy() == nil {
		_ = system(ctx, "start", Service)
	}
}
func retireLegacy(ctx context.Context, base string) error {
	// The known original data are no longer active. Remove them as their owner.
	if e := command(ctx, "/usr/sbin/runuser", "-u", "tinywarden-agent", "--", filepath.Join(base, "current", "tinywarden-agent"), "__remove-legacy"); e != nil {
		return errors.New("managed agent confirmed; legacy data cleanup requires inspection")
	}
	if e := os.Remove(legacyData); e != nil {
		return e
	}
	for _, p := range []string{"/etc/tinywarden-agent/agent.json", "/usr/local/bin/tinywarden-agent"} {
		if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	// Retain root-owned legacy SDK files if they fail exact trusted verification.
	if e := (sdk.Client{Assets: "/usr/local/lib/tinywarden-agent/runtime"}).VerifyAssets(); e != nil {
		return errors.New("managed agent confirmed; legacy runtime cleanup requires inspection")
	}
	return os.RemoveAll("/usr/local/lib/tinywarden-agent/runtime")
}
