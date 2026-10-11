package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/platform"
	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skills/runtime"
)

type platformCounter struct {
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}
type platformResult struct {
	policy *platform.Policy
	err    error
}
type platformLane struct {
	config           Config
	store            *Store
	state            *State
	result           chan platformResult
	working, sending bool
	due              time.Time
	counter          platformCounter
}

func newPlatformLane(config Config, store *Store, state *State) *platformLane {
	l := &platformLane{config: config, store: store, state: state, result: make(chan platformResult, 1)}
	// This auxiliary counter never changes enrollment, heartbeat or skill state.
	if b, e := os.ReadFile(filepath.Join(store.dir, "platform-report.json")); e == nil && len(b) <= 512 {
		_ = platform.Decode(b, &l.counter)
	}
	return l
}
func (l *platformLane) tick(ctx context.Context) error {
	select {
	case r := <-l.result:
		l.working = false
		l.due = time.Now().Add(time.Minute)
		if r.err != nil {
			l.due = time.Now().Add(5 * time.Minute)
			return nil
		}
		if r.policy == nil {
			l.sending = false
			return nil
		}
		p := r.policy
		s := *l.state
		if p.Schema != 1 || p.HostID != s.HostID || p.AgentID != s.AgentID || p.Generation != s.Generation {
			return nil
		}
		if l.counter.Generation != s.Generation {
			l.counter = platformCounter{Generation: s.Generation}
		}
		if p.Watermark > l.counter.Sequence {
			l.counter.Sequence = p.Watermark
		}
		if l.counter.Sequence >= 9007199254740990 {
			return nil
		}
		l.counter.Sequence++
		if l.saveCounter() != nil {
			return nil
		}
		sequence := l.counter.Sequence
		l.working = true
		l.sending = true
		go func() { l.result <- platformResult{err: l.report(ctx, s, sequence)} }()
	default:
	}
	if !l.working && !time.Now().Before(l.due) {
		l.working = true
		s := *l.state
		go func() {
			var p platform.Policy
			r := platform.PolicyRequest{Schema: 1, HostID: s.HostID, AgentID: s.AgentID, Generation: s.Generation, Version: Version, Managed: l.config.Managed}
			e := platform.Post(ctx, platform.HTTP(false), l.config.ControlPlaneOrigin, "/api/v1/agent/update-policy", s.Credential, r, &p, 32768)
			l.result <- platformResult{policy: &p, err: e}
		}()
	}
	// Optional maintenance failures never stop, delay or fake host contact.
	return nil
}
func (l *platformLane) report(ctx context.Context, s State, seq uint64) error {
	automatic := false
	base := filepath.Dir(l.config.Path())
	if l.config.Updates != nil {
		automatic = l.config.Updates.Automatic
	}
	if l.config.Managed {
		if b, e := platform.ReadPublic(filepath.Join(base, "config.json"), 8192); e == nil {
			var c Config
			if platform.Decode(b, &c) == nil && c.Updates != nil {
				automatic = c.Updates.Automatic
			}
		}
	}
	o, e := platform.Identity(base, l.config.Managed, automatic)
	if e != nil {
		o.Managed = false
		o.Updater = platform.UpdateStatus{Outcome: "failed", Error: "identity_invalid"}
	}
	if l.config.Managed && (skillruntime.Client{Assets: l.config.RuntimeAssets}).VerifyAssets() != nil {
		o.RuntimeSHA256 = ""
		o.Updater = platform.UpdateStatus{Outcome: "failed", Error: "runtime_invalid"}
	}
	request := platform.StatusRequest{Schema: 1, HostID: s.HostID, AgentID: s.AgentID, Generation: s.Generation, Sequence: seq, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Platform: o}
	var response struct {
		Schema   int  `json:"schema_version"`
		Accepted bool `json:"accepted"`
	}
	return platform.Post(ctx, platform.HTTP(false), l.config.ControlPlaneOrigin, "/api/v1/agent/platform-status", s.Credential, request, &response, 2048)
}
func (l *platformLane) saveCounter() error {
	b, e := json.Marshal(l.counter)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(l.store.dir, ".platform-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), filepath.Join(l.store.dir, "platform-report.json")); e != nil {
		return e
	}
	directory, e := os.Open(l.store.dir)
	if e != nil {
		return e
	}
	defer directory.Close()
	return directory.Sync()
}
