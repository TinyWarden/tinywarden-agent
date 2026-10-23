package installation

import (
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func account() (int, int, error) {
	u, e := user.Lookup("tinywarden-agent")
	if e != nil {
		return 0, 0, errors.New("service account is missing")
	}
	uid, e := strconv.Atoi(u.Uid)
	if e != nil || uid == 0 {
		return 0, 0, errors.New("invalid service account")
	}
	gid, e := strconv.Atoi(u.Gid)
	return uid, gid, e
}
func config(base string) (agentconfig.Config, error) {
	var c agentconfig.Config
	if e := safePath(base, false); e != nil {
		return c, e
	}
	b, e := agentrelease.ReadPublic(filepath.Join(base, "config.json"), 8192)
	if e != nil {
		return c, e
	}
	if e = agentrelease.Decode(b, &c); e != nil {
		return c, e
	}
	if _, e = controlplane.Origin(c.ControlPlaneOrigin); e != nil {
		return c, e
	}
	if !c.Managed || c.StateDir != filepath.Join(base, "data") || c.Updates == nil || !agentrelease.UUIDPattern.MatchString(c.Updates.HostID) || !agentrelease.UUIDPattern.MatchString(c.Updates.AgentID) {
		return c, errors.New("managed configuration is incomplete; finish installation first")
	}
	return c, nil
}
func saveConfig(base string, c agentconfig.Config) error {
	_, gid, e := account()
	if e != nil {
		return e
	}
	path := filepath.Join(base, "config.json")
	if e = atomicFile(path, c, 0640); e != nil {
		return e
	}
	return os.Chown(path, 0, gid)
}

var credentialPattern = regexp.MustCompile(`^tw_a_[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}\.[A-Za-z0-9_-]{43}$`)

func identity(base string, c agentconfig.Config) (agentstate.State, error) {
	var s agentstate.State
	uid, _, e := account()
	if e != nil {
		return s, e
	}
	info, e := os.Lstat(filepath.Join(base, "data"))
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(uid) {
		return s, errors.New("unsafe agent data directory")
	}
	path := filepath.Join(base, "data", "state.json")
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return s, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Size() > 16384 || i.Mode().Perm() != 0600 || i.Sys().(*syscall.Stat_t).Uid != uint32(uid) {
		return s, errors.New("unsafe agent identity")
	}
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil {
		return s, e
	}
	if e = agentrelease.Decode(b, &s); e != nil {
		return s, e
	}
	if s.Version != 1 || s.Generation == 0 || s.Generation > 9007199254740991 || !credentialPattern.MatchString(s.Credential) || !agentrelease.UUIDPattern.MatchString(s.HostID) || !agentrelease.UUIDPattern.MatchString(s.AgentID) || c.Updates.HostID != "" && s.HostID != c.Updates.HostID || c.Updates.AgentID != "" && s.AgentID != c.Updates.AgentID || s.Enrollment != nil || s.Replacement != nil {
		return s, errors.New("agent identity does not match the root-owned binding")
	}
	return s, nil
}
