package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

type ReconnectMessages struct {
	ScopeMismatch string `json:"scopeMismatch"`
	Unfinished    string `json:"unfinished"`
	Arguments     string `json:"arguments"`
	Completed     string `json:"completed"`
}

var errReconnectScope = errors.New("reconnect_scope_mismatch")

type reconnectIntent struct {
	Schema     int    `json:"schema_version"`
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Purpose    string `json:"purpose"`
	Generation string `json:"generation"`
}

func checkReconnect(origin string, setup controlplane.Setup, host, agent string, generation uint64, intent reconnectIntent) error {
	if setup.Origin != origin || intent.Schema != 1 || intent.HostID != host || intent.AgentID != agent || intent.Generation != strconv.FormatUint(generation, 10) || (intent.Purpose != "replace" && intent.Purpose != "restore") {
		return errReconnectScope
	}
	return nil
}

// Reconnect preserves the managed identity and existing replay-safe replacement journal.
func Reconnect(ctx context.Context, base, codeFile string, copy ReconnectMessages) error {
	c, e := config(base)
	if e != nil {
		return e
	}
	guard, e := lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	s, e := identity(base, c)
	if e != nil {
		return e
	}
	release, _, e := current(base)
	if e != nil {
		return e
	}
	code, e := readSetup(codeFile, nil)
	if e != nil {
		return e
	}
	setup, e := controlplane.ParseSetup(code)
	if e != nil {
		return e
	}
	if setup.Origin != c.ControlPlaneOrigin {
		return errors.New(copy.ScopeMismatch)
	}
	var intent reconnectIntent
	if e = controlplane.Post(ctx, controlplane.HTTP(false), setup.Origin, "/api/v1/agent/reconnect-intent", setup.Token, map[string]int{"schema_version": 1}, &intent, 2048); e != nil {
		return e
	}
	if e = checkReconnect(c.ControlPlaneOrigin, setup, s.HostID, s.AgentID, s.Generation, intent); e != nil {
		return errors.New(copy.ScopeMismatch)
	}
	uid, gid, e := account()
	if e != nil {
		return e
	}
	token, e := os.CreateTemp(c.StateDir, ".reconnect-token-")
	if e != nil {
		return e
	}
	path := token.Name()
	defer os.Remove(path)
	if _, e = token.Write([]byte(setup.Token)); e != nil {
		token.Close()
		return e
	}
	if e = token.Chown(uid, gid); e != nil {
		token.Close()
		return e
	}
	if e = token.Close(); e != nil {
		return e
	}
	if e = system(ctx, "stop", Service); e != nil {
		return e
	}
	defer func() {
		restart, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		_ = system(restart, "start", Service)
	}()
	if e = command(ctx, "/usr/sbin/runuser", "-u", "tinywarden-agent", "--", filepath.Join(release, "tinywarden-agent"), "replace", "--config", filepath.Join(base, "config.json"), "--token-file", path); e != nil {
		return errors.New(copy.Unfinished)
	}
	return nil
}
