package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

type RecoveryCoordinator struct {
	Base     string
	Registry Registry
	Call     RecoveryTransport
	// Verify validates the signed installation, old retained identity, idle cgroups
	// and updater transaction under the already held common mutation guard.
	Verify func(RecoveryScope) error
}

func (c RecoveryCoordinator) inspector(s RecoveryScope) (Provider, error) {
	definition, e := c.Registry.ResolveIntent(s.Intent())
	if e != nil {
		return nil, e
	}
	return definition.New(), nil
}
func (c RecoveryCoordinator) inspect(ctx context.Context, s RecoveryScope, commit func(RecoveryAudit) error) error {
	if c.Verify == nil {
		return errors.New("maintenance_guard_required")
	}
	if e := c.Verify(s); e != nil {
		return e
	}
	provider, e := c.inspector(s)
	if e != nil {
		return e
	}
	commitFacts := func(facts any) error {
		if e := c.Verify(s); e != nil {
			return e
		}
		boot, _, e := BootClock()
		if e != nil {
			return e
		}
		audit := RecoveryAudit{boot, "clean", false, facts}
		if e = audit.ValidateForBinding(s.Binding); e != nil {
			return e
		}
		return commit(audit)
	}
	if s.Binding != nil {
		inspector, ok := provider.(ActionInspector)
		if !ok {
			return errors.New("maintenance_recovery_unsupported")
		}
		return inspector.InspectAction(ctx, func(raw json.RawMessage) error {
			facts, err := NewActionAuditFacts(raw)
			if err != nil {
				return err
			}
			return commitFacts(facts)
		})
	}
	inspector, ok := provider.(Inspector)
	if !ok {
		return errors.New("maintenance_recovery_unsupported")
	}
	return inspector.Inspect(ctx, func(facts map[string]string) error { return commitFacts(facts) })
}
func (c RecoveryCoordinator) localScope(s RecoveryScope) (string, *string, error) {
	if s.Validate() != nil {
		return "", nil, errors.New("maintenance_scope_changed")
	}
	fence, e := hostmutation.ReadFence(c.Base)
	if e != nil {
		return "", nil, e
	}
	expected := s.Intent().Fence()
	if fence == nil || *fence != expected {
		return "", nil, errors.New("maintenance_scope_changed")
	}
	j, e := Load(c.Base)
	if os.IsNotExist(e) {
		return "orphan_fence", nil, nil
	}
	if e != nil {
		return "", nil, e
	}
	if j.Acknowledged {
		return "", nil, errors.New("maintenance_reconciliation_required")
	}
	if e = matchRecoveryJournal(*j, s); e != nil {
		return "", nil, e
	}
	return "present", j.ApplyAttempt, nil
}
func (c RecoveryCoordinator) prepare(ctx context.Context, token string) (*RecoveryRecord, error) {
	if !RecoveryTokenValid(token) {
		return nil, errors.New("maintenance_recovery_token_invalid")
	}
	reply, e := c.Call(ctx, token, "describe", struct {
		Schema int `json:"schema_version"`
	}{2})
	if e != nil {
		return nil, e
	}
	if reply.Validate() != nil || reply.State != "issued" {
		return nil, errors.New("maintenance_recovery_invalid")
	}
	match := recoveryTokenPattern.FindStringSubmatch(token)
	if match[1] != reply.RecoveryID {
		return nil, errors.New("maintenance_scope_changed")
	}
	kind, attempt, e := c.localScope(reply.Scope)
	if e != nil {
		return nil, e
	}
	r := &RecoveryRecord{Schema: reply.Scope.Schema(), Token: token, State: "prepared", Acceptance: RecoveryAcceptance{Schema: reply.Scope.Schema(), Scope: reply.Scope, LocalAttempt: attempt, JournalState: kind}}
	e = c.inspect(ctx, reply.Scope, func(a RecoveryAudit) error { r.Acceptance.Audit = a; return SaveRecovery(c.Base, *r) })
	return r, e
}

// Reconcile is explicit local inspection. It never calls Prepare/Fetch/Apply/Recover.
// Caller holds the shared root guard. No native package locks span HTTP.
func (c RecoveryCoordinator) Reconcile(ctx context.Context, token string) error {
	r, e := LoadRecovery(c.Base)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if r != nil && r.State == "finalized" {
		if token == "" {
			return nil
		}
		if e = c.archive(*r); e != nil {
			return e
		}
		r = nil
	}
	if r == nil {
		r, e = c.prepare(ctx, token)
		if e != nil {
			return e
		}
	}
	if r.State == "prepared" && token != "" && token != r.Token {
		// A newly issued token supersedes only an uncommitted local proposal. The app
		// refuses issuance once acceptance committed, including a lost response.
		r, e = c.prepare(ctx, token)
		if e != nil {
			return e
		}
	}
	if token != "" && token != r.Token {
		return errors.New("maintenance_recovery_pending")
	}
	if r.State == "prepared" {
		reply, e := c.Call(ctx, r.Token, "accept", r.Acceptance)
		if e != nil {
			return e
		}
		if reply.Validate() != nil || reply.State == "issued" || !sameValue(reply.Scope, r.Acceptance.Scope) {
			return errors.New("maintenance_recovery_invalid")
		}
		r.Accepted = &reply
		r.State = "accepted"
		if e = SaveRecovery(c.Base, *r); e != nil {
			return e
		}
	}
	if r.State == "accepted" || r.State == "release_intent" {
		if e = c.release(ctx, r); e != nil {
			return e
		}
	}
	return c.deliver(ctx, r)
}
func (c RecoveryCoordinator) release(ctx context.Context, r *RecoveryRecord) error {
	fence, e := hostmutation.ReadFence(c.Base)
	if e != nil {
		return e
	}
	if fence == nil {
		if r.State != "release_intent" || releasedJournal(c.Base, r) != nil {
			return errors.New("maintenance_reconciliation_required")
		}
		r.State = "released"
		return SaveRecovery(c.Base, *r)
	}
	expected := r.Acceptance.Scope.Intent().Fence()
	if *fence != expected {
		return errors.New("maintenance_scope_changed")
	}
	return c.inspect(ctx, r.Acceptance.Scope, func(audit RecoveryAudit) error {
		r.Finalization = &RecoveryFinalization{r.Acceptance.Scope.Schema(), r.Acceptance.Scope.RecoveryID, *r.Accepted.AcceptanceSHA, LocalRelease{r.Acceptance.Scope.JobID, r.Acceptance.LocalAttempt, true, audit}}
		r.State = "release_intent"
		if e := SaveRecovery(c.Base, *r); e != nil {
			return e
		}
		if e := closeRecoveredJournal(c.Base, r); e != nil {
			return e
		}
		if e := hostmutation.ClearFence(c.Base, expected); e != nil {
			return e
		}
		r.State = "released"
		return SaveRecovery(c.Base, *r)
	})
}
func (c RecoveryCoordinator) deliver(ctx context.Context, r *RecoveryRecord) error {
	if r.State == "finalized" {
		return nil
	}
	if r.State != "released" || releasedJournal(c.Base, r) != nil {
		return errors.New("maintenance_reconciliation_required")
	}
	reply, e := c.Call(ctx, r.Token, "finalize", r.Finalization)
	if e != nil {
		return e
	}
	if reply.Validate() != nil || reply.State != "finalized" || !sameValue(reply.Scope, r.Acceptance.Scope) {
		return errors.New("maintenance_recovery_invalid")
	}
	r.Finalized = &reply
	r.State = "finalized"
	r.Token = ""
	return SaveRecovery(c.Base, *r)
}

// RetryRecoveryDelivery cannot initiate an audit, clear a fence or execute work.
func (c RecoveryCoordinator) RetryRecoveryDelivery(ctx context.Context) error {
	r, e := LoadRecovery(c.Base)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if r.State != "released" && r.State != "finalized" {
		return nil
	}
	return c.deliver(ctx, r)
}
func (c RecoveryCoordinator) archive(r RecoveryRecord) error {
	if r.State != "finalized" {
		return errors.New("maintenance_recovery_pending")
	}
	dir := filepath.Join(c.Base, "updater", "private", "maintenance-history")
	if e := os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	raw, e := readPrivate(recoveryPath(c.Base), MaxRecoveryRecord)
	if e != nil {
		return e
	}
	return hostmutation.PrivateWrite(filepath.Join(dir, r.Acceptance.Scope.RecoveryID+"-recovery.json"), raw)
}
