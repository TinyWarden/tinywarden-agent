package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

type inspectionProvider struct {
	countingProvider
	inspections int
	dirty       bool
}

func (p *inspectionProvider) Inspect(_ context.Context, commit func(map[string]string) error) error {
	p.inspections++
	if p.dirty {
		return errors.New("dirty native state")
	}
	return commit(map[string]string{"state_sha256": strings.Repeat("a", 64), "selection_sha256": strings.Repeat("b", 64)})
}

type recoveryWire struct {
	t             *testing.T
	base          string
	reply         RecoveryReply
	accept, final string
	lose          string
	lost          bool
}

func (w *recoveryWire) call(_ context.Context, _ string, mode string, input any) (RecoveryReply, error) {
	switch mode {
	case "describe":
		return w.reply, nil
	case "accept":
		sha, e := recoveryDigest(input)
		if e != nil {
			return RecoveryReply{}, e
		}
		if w.accept != "" && w.accept != sha {
			return RecoveryReply{}, errors.New("changed acceptance")
		}
		if fence, e := hostmutation.ReadFence(w.base); e != nil || fence == nil {
			w.t.Fatal("app accepted without local fence")
		}
		w.accept = sha
		w.reply.State = "accepted"
		w.reply.AcceptanceSHA = &sha
	case "finalize":
		sha, e := recoveryDigest(input)
		if e != nil {
			return RecoveryReply{}, e
		}
		if w.final != "" && w.final != sha {
			return RecoveryReply{}, errors.New("changed finalization")
		}
		if fence, e := hostmutation.ReadFence(w.base); e != nil || fence != nil {
			w.t.Fatal("app finalized before durable local release")
		}
		j, e := Load(w.base)
		if e != nil || !j.Acknowledged || j.Recovery == nil {
			w.t.Fatal("old work remained executable")
		}
		w.final = sha
		w.reply.State = "finalized"
		w.reply.FinalizationSHA = &sha
	default:
		return RecoveryReply{}, errors.New("unknown recovery step")
	}
	if w.lose == mode && !w.lost {
		w.lost = true
		return RecoveryReply{}, errors.New("lost committed reply")
	}
	return w.reply, nil
}
func recoveryFixture(t *testing.T) (RecoveryCoordinator, *inspectionProvider, *recoveryWire, string, *Journal) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root on designated disposable host")
	}
	base := t.TempDir()
	if e := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); e != nil {
		t.Fatal(e)
	}
	guard, e := hostmutation.Lock(base)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { guard.Close() })
	intent := fixtureIntent()
	j, e := Begin(base, intent)
	if e != nil {
		t.Fatal(e)
	}
	reason := "maintenance_worker_interrupted"
	attempt := "44444444-4444-4444-8444-444444444444"
	j.Phase = "terminal"
	j.Attention = true
	j.ApplyAttempt = &attempt
	j.Result = &Result{Effect: "unknown", Reason: &reason, ProcessRunning: true, Facts: json.RawMessage(`{}`)}
	if e = Save(base, *j); e != nil {
		t.Fatal(e)
	}
	scope := RecoveryScope{HostID: intent.Scope.HostID, AgentID: intent.Scope.AgentID, Generation: intent.Scope.Generation, RecoveryID: "55555555-5555-4555-8555-555555555555", JobID: intent.ID, OperationID: "66666666-6666-4666-8666-666666666666", Action: intent.Action, ProviderVersion: 1, ExpiresAt: intent.ExpiresAt, Revision: "2", ApplyAttempt: &attempt}
	wire := &recoveryWire{t: t, base: base, reply: RecoveryReply{Schema: 1, RecoveryID: scope.RecoveryID, Scope: scope, State: "issued", ExpiresAt: time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339Nano)}}
	provider := &inspectionProvider{}
	coordinator := RecoveryCoordinator{Base: base, Registry: Registry{{ID: intent.Action, Version: 1, New: func() Provider { return provider }}}, Call: wire.call,
		Verify: func(s RecoveryScope) error {
			if !sameValue(s.ScopeValue(), intent.Scope) {
				return errors.New("wrong installation")
			}
			return nil
		}}
	token := "tw_recovery_v1." + scope.RecoveryID + "." + strings.Repeat("c", 43)
	return coordinator, provider, wire, token, j
}
func assertNoExecution(t *testing.T, p *inspectionProvider) {
	t.Helper()
	if p.prepared+p.fetch+p.apply+p.recover != 0 {
		t.Fatal("reconciliation invoked execution or successful-receipt recovery")
	}
}

func explainRecovery(t *testing.T, base string) {
	t.Helper()
	raw, e := readPrivate(recoveryPath(base), MaxControl)
	if e != nil {
		t.Log("recovery file:", e)
		return
	}
	var r RecoveryRecord
	if e = controlplane.Decode(raw, &r); e != nil {
		t.Log("decode:", e)
		return
	}
	sha, _ := recoveryDigest(r.Acceptance)
	t.Logf("saved state=%s valid=%v local_attempt_matches=%v audit=%v", r.State, r.Validate(), sameValue(r.Acceptance.LocalAttempt, r.Acceptance.Scope.ApplyAttempt), r.Acceptance.Audit.Validate())
	if r.Accepted != nil {
		t.Logf("accepted state=%s valid=%v scope_matches=%v hash_matches=%v", r.Accepted.State, r.Accepted.Validate(), sameValue(r.Accepted.Scope, r.Acceptance.Scope), r.Accepted.AcceptanceSHA != nil && *r.Accepted.AcceptanceSHA == sha)
	}
}
