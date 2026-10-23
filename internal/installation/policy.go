package installation

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

func policy(ctx context.Context, base string, c agentconfig.Config, version string) (agentrelease.Policy, error) {
	var p agentrelease.Policy
	s, e := identity(base, c)
	if e != nil {
		return p, e
	}
	r := agentrelease.PolicyRequest{Schema: 1, HostID: s.HostID, AgentID: s.AgentID, Generation: s.Generation, Version: version, Managed: true}
	if e = controlplane.Post(ctx, controlplane.HTTP(false), c.ControlPlaneOrigin, "/api/v1/agent/update-policy", s.Credential, r, &p, 32768); e != nil {
		return p, e
	}
	expiry, e := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if e != nil || !expiry.After(time.Now()) || expiry.After(time.Now().Add(5*time.Minute+time.Second)) || p.Schema != 1 || p.HostID != s.HostID || p.AgentID != s.AgentID || p.Generation != s.Generation {
		return p, errors.New("invalid app policy")
	}
	if !agentrelease.Supports(p.Capabilities, []string{agentrelease.Capability}) {
		return p, errors.New("app upgrade required")
	}
	return p, nil
}
func current(base string) (string, agentrelease.Manifest, error) {
	var m agentrelease.Manifest
	b, e := agentrelease.ReadPublic(filepath.Join(base, "updater", "receipt.json"), 32768)
	if e != nil {
		return "", m, e
	}
	var c agentrelease.Candidate
	if e = agentrelease.Decode(b, &c); e != nil {
		return "", m, e
	}
	m, e = agentrelease.Verify([]byte(c.Manifest), c.Signature)
	if e != nil {
		return "", m, e
	}
	link, e := filepath.EvalSymlinks(filepath.Join(base, "current"))
	if e != nil || link != filepath.Join(base, "releases", m.Version) {
		return "", m, errors.New("installed release receipt mismatch")
	}
	if e = verifyDirectory(link, m); e != nil {
		return "", m, e
	}
	return link, m, nil
}
func confirm(ctx context.Context, base string, c agentconfig.Config, m agentrelease.Manifest) error {
	started := time.Now().Add(-time.Second)
	for attempt := 0; attempt < 15; attempt++ {
		p, e := policy(ctx, base, c, m.Version)
		if e == nil && p.Reported != nil {
			r := p.Reported
			observed, _ := time.Parse(time.RFC3339Nano, r.ObservedAt)
			if observed.After(started) && r.Version == m.Version && r.BinarySHA256 == m.BinarySHA256 && r.RuntimeSHA256 == m.RuntimeSHA256 && r.DescriptorSHA256 == m.DescriptorSHA256 && mainProcess(ctx, base, m.Version) {
				return status(base, agentrelease.UpdateStatus{Outcome: "updated", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), Target: m.Version})
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
	}
	return errors.New("installed; waiting for a fresh monitoring-process report; run check later")
}

func mainProcess(ctx context.Context, base, version string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", Service, "--value", "-p", "MainPID").Output()
	pid, e2 := strconv.Atoi(strings.TrimSpace(string(b)))
	if e != nil || e2 != nil || pid < 1 {
		return false
	}
	exe, e := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	return e == nil && exe == filepath.Join(base, "releases", version, "tinywarden-agent")
}
