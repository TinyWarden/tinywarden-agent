package maintenance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

type RecoveryClosure struct {
	RecoveryID    string `json:"recovery_id"`
	AcceptanceSHA string `json:"acceptance_sha256"`
	ReleaseSHA    string `json:"release_sha256"`
}
type RecoveryRecord struct {
	Schema       int                   `json:"schema_version"`
	Token        string                `json:"token"`
	State        string                `json:"state"`
	Acceptance   RecoveryAcceptance    `json:"acceptance"`
	Accepted     *RecoveryReply        `json:"accepted"`
	Finalization *RecoveryFinalization `json:"finalization"`
	Finalized    *RecoveryReply        `json:"finalized"`
}

var recoveryTokenPattern = regexp.MustCompile(`^tw_recovery_v1\.([a-f0-9-]{36})\.[A-Za-z0-9_-]{43}$`)

func RecoveryTokenValid(token string) bool { return recoveryTokenPattern.MatchString(token) }
func (r RecoveryRecord) Validate() error {
	bad := errors.New("maintenance_recovery_invalid")
	a := r.Acceptance
	if r.Schema != 1 || a.Schema != 1 || a.Scope.Validate() != nil || a.Audit.Validate() != nil || (a.JournalState != "present" && a.JournalState != "orphan_fence") {
		return bad
	}
	if a.LocalAttempt != nil && !sameValue(a.LocalAttempt, a.Scope.ApplyAttempt) || a.JournalState == "orphan_fence" && a.LocalAttempt != nil {
		return bad
	}
	if r.State != "prepared" && r.State != "accepted" && r.State != "release_intent" && r.State != "released" && r.State != "finalized" {
		return bad
	}
	if r.State != "finalized" {
		m := recoveryTokenPattern.FindStringSubmatch(r.Token)
		if len(m) != 2 || m[1] != a.Scope.RecoveryID {
			return bad
		}
	} else if r.Token != "" {
		return bad
	}
	if (r.State == "prepared") != (r.Accepted == nil) {
		return bad
	}
	if r.Accepted != nil {
		sha, e := recoveryDigest(a)
		if e != nil || r.Accepted.Validate() != nil || r.Accepted.State == "issued" || !sameValue(r.Accepted.Scope, a.Scope) || r.Accepted.AcceptanceSHA == nil || *r.Accepted.AcceptanceSHA != sha {
			return bad
		}
	}
	needsFinal := r.State == "release_intent" || r.State == "released" || r.State == "finalized"
	if needsFinal != (r.Finalization != nil) {
		return bad
	}
	if r.Finalization != nil {
		f := r.Finalization
		if f.Schema != 1 || f.RecoveryID != a.Scope.RecoveryID || r.Accepted == nil || f.AcceptanceSHA != *r.Accepted.AcceptanceSHA || f.LocalRelease.JobID != a.Scope.JobID || !f.LocalRelease.Released || !sameValue(f.LocalRelease.LocalAttempt, a.LocalAttempt) || f.LocalRelease.Audit.Validate() != nil {
			return bad
		}
	}
	if (r.State == "finalized") != (r.Finalized != nil) {
		return bad
	}
	if r.Finalized != nil {
		sha, e := recoveryDigest(r.Finalization)
		if e != nil || r.Finalized.Validate() != nil || r.Finalized.State != "finalized" || !sameValue(r.Finalized.Scope, a.Scope) || r.Finalized.FinalizationSHA == nil || *r.Finalized.FinalizationSHA != sha {
			return bad
		}
	}
	return nil
}
func recoveryPath(base string) string {
	return filepath.Join(base, "updater", "private", "maintenance-recovery.json")
}
func LoadRecovery(base string) (*RecoveryRecord, error) {
	raw, e := readPrivate(recoveryPath(base), MaxControl)
	if e != nil {
		return nil, e
	}
	var r RecoveryRecord
	if controlplane.Decode(raw, &r) != nil || r.Validate() != nil {
		return nil, errors.New("maintenance_recovery_invalid")
	}
	return &r, nil
}
func SaveRecovery(base string, r RecoveryRecord) error {
	if r.Validate() != nil {
		return errors.New("maintenance_recovery_invalid")
	}
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > MaxControl {
		return errors.New("maintenance_recovery_invalid")
	}
	return hostmutation.PrivateWrite(recoveryPath(base), raw)
}
func PendingRecovery(base string) (bool, error) {
	r, e := LoadRecovery(base)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return r.State != "finalized", nil
}
func (c RecoveryClosure) Valid() bool {
	return uuidPattern.MatchString(c.RecoveryID) && digestPattern.MatchString(c.AcceptanceSHA) && digestPattern.MatchString(c.ReleaseSHA)
}

// A recovery closure is separate from the original package result.
func closeRecoveredJournal(base string, r *RecoveryRecord) error {
	sha, e := recoveryDigest(r.Finalization)
	if e != nil {
		return e
	}
	closure := &RecoveryClosure{r.Acceptance.Scope.RecoveryID, *r.Accepted.AcceptanceSHA, sha}
	j, e := Load(base)
	if os.IsNotExist(e) && r.Acceptance.JournalState == "orphan_fence" {
		s := r.Acceptance.Scope
		reason := "maintenance_reconciled_unknown"
		j = &Journal{Version: 1, Intent: Intent{s.JobID, s.ScopeValue(), s.Action, s.ProviderVersion, s.ExpiresAt}, Result: &Result{Effect: "unknown", Reason: &reason, Facts: json.RawMessage(`{}`)}}
	} else if e != nil {
		return e
	}
	if e = matchRecoveryJournal(*j, r.Acceptance.Scope); e != nil {
		return e
	}
	j.Phase = "terminal"
	j.Acknowledged = true
	j.Attention = false
	j.Recovery = closure
	j.Pending = nil
	j.PendingOrigin = nil
	j.Unsent = ""
	if j.Result == nil {
		reason := "maintenance_reconciled_unknown"
		j.Result = &Result{Effect: "unknown", Reason: &reason, Facts: json.RawMessage(`{}`)}
	}
	return Save(base, *j)
}
func releasedJournal(base string, r *RecoveryRecord) error {
	j, e := Load(base)
	if e != nil {
		return e
	}
	sha, e := recoveryDigest(r.Finalization)
	if e != nil {
		return e
	}
	if !j.Acknowledged || j.Recovery == nil || j.Recovery.RecoveryID != r.Acceptance.Scope.RecoveryID || j.Recovery.ReleaseSHA != sha || j.Intent.ID != r.Acceptance.Scope.JobID {
		return errors.New("maintenance_reconciliation_required")
	}
	return nil
}
func matchRecoveryJournal(j Journal, s RecoveryScope) error {
	if j.Intent.ID != s.JobID || j.Intent.Scope != s.ScopeValue() || j.Intent.Action != s.Action || j.Intent.ProviderVersion != s.ProviderVersion {
		return errors.New("maintenance_scope_changed")
	}
	for _, pair := range [][2]*string{{j.PlanSHA, s.ProviderSHA}, {j.EnvelopeSHA, s.PlanSHA}, {j.PlanID, s.PlanID}, {j.ApprovalID, s.ApprovalID}, {j.ApplyAttempt, s.ApplyAttempt}} {
		// Preparation may have been saved locally before its upload reached the app.
		// This preserves extra root evidence without inventing an approval/attempt.
		if pair[1] == nil && s.ApplyAttempt == nil && j.ApplyAttempt == nil && (j.Phase == "preparing" || j.Phase == "awaiting_approval" || j.Phase == "terminal") {
			continue
		}
		if pair[0] != nil && !sameValue(pair[0], pair[1]) {
			return errors.New("maintenance_scope_changed")
		}
	}
	return nil
}
