package installation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

type InstallOptions struct {
	Base, Archive, Manifest, Signature, CodeFile string
	Automatic, Adopt                             bool
}

func validTarget(v string) bool { return agentrelease.ValidVersion(v) }
func verifiedInput(o InstallOptions) (agentrelease.Manifest, agentrelease.Candidate, error) {
	var m agentrelease.Manifest
	var c agentrelease.Candidate
	b, e := os.ReadFile(o.Manifest)
	if e != nil || len(b) > 16384 {
		return m, c, errors.New("release manifest is missing or oversized")
	}
	sig, e := os.ReadFile(o.Signature)
	if e != nil || len(sig) > 256 {
		return m, c, errors.New("release signature is missing or oversized")
	}
	c = agentrelease.Candidate{Manifest: string(b), Signature: string(sig)}
	m, e = agentrelease.Verify(b, c.Signature)
	return m, c, e
}
func prepare(base string) error {
	for _, p := range []string{base, filepath.Join(base, "releases"), filepath.Join(base, "updater"), filepath.Join(base, "updater", "private")} {
		mode := os.FileMode(0755)
		if filepath.Base(p) == "private" {
			mode = 0700
		}
		if e := rootDirectory(p, mode); e != nil {
			return e
		}
		if e := safePath(p, false); e != nil {
			return e
		}
	}
	return nil
}
func stage(base, archive string, m agentrelease.Manifest) (string, error) {
	path, e := os.MkdirTemp(filepath.Join(base, "updater", "private"), "stage-")
	if e != nil {
		return "", e
	}
	if e = extract(archive, path, m); e != nil {
		os.RemoveAll(path)
		return "", e
	}
	if e = os.Chmod(path, 0755); e != nil {
		return "", e
	}
	final := filepath.Join(base, "releases", m.Version)
	if _, e = os.Lstat(final); !os.IsNotExist(e) {
		os.RemoveAll(path)
		return "", errors.New("target release already exists")
	}
	if e = os.Rename(path, final); e != nil {
		return "", e
	}
	return final, syncDirectory(filepath.Dir(final))
}
func Install(ctx context.Context, o InstallOptions) error {
	m, receipt, e := verifiedInput(o)
	if e != nil {
		return e
	}
	if e = Preflight(o.Base, true); e != nil {
		return e
	}
	var setup controlplane.Setup
	if !o.Adopt {
		code, e := readSetup(o.CodeFile)
		if e != nil {
			return e
		}
		setup, e = controlplane.ParseSetup(code)
		if e != nil {
			return e
		}
		fmt.Printf("Connecting to %s\n", setup.Origin)
		var caps struct {
			Schema       int      `json:"schema_version"`
			Capabilities []string `json:"capabilities"`
		}
		if e = controlplane.Post(ctx, controlplane.HTTP(false), setup.Origin, "/api/v1/agent/setup", setup.Token, map[string]int{"schema_version": 1}, &caps, 2048); e != nil {
			return e
		}
		if caps.Schema != 1 || !agentrelease.Supports(caps.Capabilities, m.Capabilities) {
			return errors.New("update the app before installing this release")
		}
	} else if e = inspectLegacy(); e != nil {
		return e
	}
	if o.Adopt {
		defer restartLegacyOnFailure(ctx)
	}
	if e = prepare(o.Base); e != nil {
		return e
	}
	guard, e := lock(o.Base)
	if e != nil {
		return e
	}
	defer guard.Close()
	if _, _, e = account(); e != nil {
		if e = command(ctx, "/usr/sbin/useradd", "--system", "--user-group", "--home-dir", filepath.Join(o.Base, "data"), "--no-create-home", "--shell", "/usr/sbin/nologin", "tinywarden-agent"); e != nil {
			return e
		}
	}
	uid, gid, e := account()
	if e != nil {
		return e
	}
	release, e := stage(o.Base, o.Archive, m)
	if e != nil {
		return e
	}
	if e = verifyUnits(ctx, o.Base, release); e != nil {
		return e
	}
	c := agentconfig.Config{Managed: true, ControlPlaneOrigin: setup.Origin, StateDir: filepath.Join(o.Base, "data"), Updates: &agentrelease.UpdateSettings{Automatic: o.Automatic}}
	if o.Adopt {
		c, e = adopt(ctx, o.Base, o.Automatic)
		if e != nil {
			return e
		}
	} else {
		if e = os.Mkdir(c.StateDir, 0700); e != nil {
			return e
		}
		if e = os.Chown(c.StateDir, uid, gid); e != nil {
			return e
		}
	}
	if e = switchCurrent(o.Base, m.Version); e != nil {
		return e
	}
	if e = saveConfig(o.Base, c); e != nil {
		return e
	}
	if e = atomicFile(filepath.Join(o.Base, "updater", "receipt.json"), receipt, 0644); e != nil {
		return e
	}
	if !o.Adopt {
		token, err := os.CreateTemp(c.StateDir, ".setup-token-")
		if err != nil {
			return err
		}
		tokenPath := token.Name()
		if _, e = token.Write([]byte(setup.Token)); e != nil {
			token.Close()
			return e
		}
		if e = token.Chown(uid, gid); e != nil {
			token.Close()
			os.Remove(tokenPath)
			return e
		}
		if e = token.Close(); e != nil {
			os.Remove(tokenPath)
			return e
		}
		defer os.Remove(tokenPath)
		if e = command(ctx, "/usr/sbin/runuser", "-u", "tinywarden-agent", "--", filepath.Join(release, "tinywarden-agent"), "enroll", "--config", filepath.Join(o.Base, "config.json"), "--token-file", tokenPath); e != nil {
			return errors.New("enrollment unfinished; preserve this installation and run repair")
		}
		if e = bind(o.Base, &c); e != nil {
			return e
		}
		if e = saveConfig(o.Base, c); e != nil {
			return e
		}
	}
	if e = atomicFile(filepath.Join(o.Base, "updater", "receipt.json"), receipt, 0644); e != nil {
		return e
	}
	if o.Adopt {
		if e = os.Remove("/etc/systemd/system/" + Service); e != nil {
			return e
		}
	}
	if e = units(ctx, o.Base, release); e != nil {
		return e
	}
	if e = status(o.Base, agentrelease.UpdateStatus{Outcome: "awaiting_confirmation", Target: m.Version}); e != nil {
		return e
	}
	if e = system(ctx, "enable", "--now", Service); e != nil {
		return e
	}
	if e = confirm(ctx, o.Base, c, m); e != nil {
		return e
	}
	if e = system(ctx, "enable", "--now", Timer); e != nil {
		return e
	}
	if o.Adopt {
		return retireLegacy(ctx, o.Base)
	}
	return nil
}
func bind(base string, c *agentconfig.Config) error {
	// Read identity only once to establish the root binding after enrollment.
	s, e := identity(base, *c)
	if e != nil {
		return e
	}
	c.Updates.HostID = s.HostID
	c.Updates.AgentID = s.AgentID
	_, e = identity(base, *c)
	return e
}
