package installation

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

type remoteIntent struct {
	HostID     string                    `json:"host_id"`
	AgentID    string                    `json:"agent_id"`
	Generation uint64                    `json:"generation"`
	Request    agentrelease.RemoteUpdate `json:"request"`
	State      string                    `json:"state"`
	Error      *string                   `json:"error"`
}

func withUpdater(base string, run func(agentconfig.Config) error) error {
	if e := Preflight(base, false); e != nil {
		return e
	}
	guard, e := lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	c, e := config(base)
	if e != nil {
		return e
	}
	return run(c)
}
func remoteActive(state string) bool {
	return state == "queued" || state == "installing" || state == "confirming"
}
func remoteReply(reply agentrelease.RemoteUpdateReply, request agentrelease.RemoteUpdateRequest, at time.Time) error {
	if reply.Schema != 1 || reply.HostID != request.HostID || reply.AgentID != request.AgentID || reply.Generation != request.Generation || reply.Capability != agentrelease.RemoteUpdateCapability {
		return errors.New("update_scope_changed")
	}
	r := reply.Request
	if r == nil {
		if request.RequestID != nil {
			return errors.New("update_request_unknown")
		}
		return nil
	}
	if !agentrelease.UUIDPattern.MatchString(r.ID) || request.RequestID != nil && r.ID != *request.RequestID || !remoteActive(r.State) && r.State != "succeeded" && r.State != "failed" {
		return errors.New("update_request_invalid")
	}
	expires, e := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if e != nil || remoteActive(r.State) && (!expires.After(at) || expires.After(at.Add(24*time.Hour+5*time.Minute))) {
		return errors.New("update_expired")
	}
	if _, e = agentrelease.Verify([]byte(r.Candidate.Manifest), r.Candidate.Signature); e != nil {
		return e
	}
	return nil
}
func remoteCall(ctx context.Context, base string, c agentconfig.Config, id *string, state string, problem *string) (*agentrelease.RemoteUpdate, error) {
	s, e := identity(base, c)
	if e != nil {
		return nil, e
	}
	request := agentrelease.RemoteUpdateRequest{Schema: 1, HostID: s.HostID, AgentID: s.AgentID, Generation: s.Generation, RequestID: id, State: state, Error: problem}
	var reply agentrelease.RemoteUpdateReply
	e = controlplane.Post(ctx, controlplane.HTTP(false), c.ControlPlaneOrigin, "/api/v1/agent/update-requests", s.Credential, request, &reply, 32768)
	if e != nil {
		return nil, e
	}
	if e = remoteReply(reply, request, time.Now()); e != nil {
		return nil, e
	}
	return reply.Request, nil
}
func remotePolicy(ctx context.Context, base string, c agentconfig.Config, expected *agentrelease.RemoteUpdate, p *agentrelease.Policy) error {
	r, e := remoteCall(ctx, base, c, &expected.ID, "poll", nil)
	if e != nil {
		return e
	}
	if r == nil || !remoteActive(r.State) || r.Candidate != expected.Candidate {
		return errors.New("update_scope_changed")
	}
	p.Candidate = &expected.Candidate
	return nil
}
func intentPath(base string) string {
	return filepath.Join(base, "updater", "private", "remote-request.json")
}
func saveIntent(base string, v remoteIntent) error { return atomicFile(intentPath(base), v, 0600) }

// Polling and ordinary/manual updates share the same root-owned installation lock.
// A persisted terminal result is acknowledged again, never installed again.
func PollUpdates(ctx context.Context, base string) error {
	return withUpdater(base, func(c agentconfig.Config) error { return pollUpdates(ctx, base, c) })
}
func pollUpdates(ctx context.Context, base string, c agentconfig.Config) error {
	s, e := identity(base, c)
	if e != nil {
		return e
	}
	work := remoteWork{
		call: func(id *string, state string, problem *string) (*agentrelease.RemoteUpdate, error) {
			return remoteCall(ctx, base, c, id, state, problem)
		},
		current: func() (agentrelease.Manifest, error) { _, m, e := current(base); return m, e },
		update: func(automatic, checkOnly bool, r *agentrelease.RemoteUpdate) error {
			return updateLocked(ctx, base, c, automatic, checkOnly, r)
		},
		confirm: func(m agentrelease.Manifest) error { return confirm(ctx, base, c, m) },
	}
	return runRemote(base, s, work)
}
