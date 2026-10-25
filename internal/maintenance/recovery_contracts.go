package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

type RecoveryScope struct {
	HostID          string                  `json:"host_id"`
	AgentID         string                  `json:"agent_id"`
	Generation      uint64                  `json:"generation"`
	RecoveryID      string                  `json:"recovery_id"`
	JobID           string                  `json:"job_id"`
	OperationID     string                  `json:"operation_id"`
	Action          string                  `json:"action"`
	ProviderVersion int                     `json:"provider_version"`
	ExpiresAt       string                  `json:"job_expires_at"`
	Revision        string                  `json:"revision"`
	PlanID          *string                 `json:"plan_id"`
	ProviderSHA     *string                 `json:"provider_sha256"`
	PlanSHA         *string                 `json:"plan_sha256"`
	ApprovalID      *string                 `json:"approval_id"`
	ApplyAttempt    *string                 `json:"apply_attempt"`
	Binding         *actioncontract.Binding `json:"binding,omitempty"`
	InputJSON       string                  `json:"input_json,omitempty"`
	SettingsJSON    string                  `json:"settings_json,omitempty"`
	Archive         *actioncontract.Archive `json:"archive,omitempty"`
}

func (s RecoveryScope) ScopeValue() Scope { return Scope{s.HostID, s.AgentID, s.Generation} }
func (s RecoveryScope) Schema() int {
	if s.Binding != nil {
		return 2
	}
	return 1
}
func (s RecoveryScope) Intent() Intent {
	return Intent{ID: s.JobID, Scope: s.ScopeValue(), Action: s.Action, ProviderVersion: s.ProviderVersion, ExpiresAt: s.ExpiresAt, Binding: s.Binding, InputJSON: s.InputJSON, SettingsJSON: s.SettingsJSON, Archive: s.Archive}
}

type RecoveryAudit struct {
	BootID         string `json:"boot_id"`
	Integrity      string `json:"integrity"`
	ProcessRunning bool   `json:"process_running"`
	Facts          any    `json:"facts"`
}
type RecoveryAcceptance struct {
	Schema       int           `json:"schema_version"`
	Scope        RecoveryScope `json:"scope"`
	LocalAttempt *string       `json:"local_attempt"`
	JournalState string        `json:"journal_state"`
	Audit        RecoveryAudit `json:"audit"`
}
type LocalRelease struct {
	JobID        string        `json:"job_id"`
	LocalAttempt *string       `json:"local_attempt"`
	Released     bool          `json:"released"`
	Audit        RecoveryAudit `json:"audit"`
}
type RecoveryFinalization struct {
	Schema        int          `json:"schema_version"`
	RecoveryID    string       `json:"recovery_id"`
	AcceptanceSHA string       `json:"acceptance_sha256"`
	LocalRelease  LocalRelease `json:"local_release"`
}
type RecoveryReply struct {
	Schema          int           `json:"schema_version"`
	RecoveryID      string        `json:"recovery_id"`
	Scope           RecoveryScope `json:"scope"`
	ExpiresAt       string        `json:"expires_at"`
	State           string        `json:"state"`
	AcceptanceSHA   *string       `json:"acceptance_sha256"`
	FinalizationSHA *string       `json:"finalization_sha256"`
}

// Inspector holds real provider locks while invoking this local-only commit.
// No network or human input belongs inside the callback.
type Inspector interface {
	Inspect(context.Context, func(map[string]string) error) error
}
type ActionInspector interface {
	InspectAction(context.Context, func(json.RawMessage) error) error
}
type RecoveryTransport func(context.Context, string, string, any) (RecoveryReply, error)

func recoveryDigest(v any) (string, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&value); e != nil {
		return "", e
	}
	raw, e = actioncontract.Canonical(value)
	if e != nil || len(raw) > MaxRecoveryRecord {
		return "", errors.New("maintenance_recovery_invalid")
	}
	// The app uses the same ASCII string escaping and integer canonical form.
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (s RecoveryScope) Validate() error {
	if s.Intent().Validate() != nil || !uuidPattern.MatchString(s.RecoveryID) || !uuidPattern.MatchString(s.OperationID) || !regexp.MustCompile(`^[1-9][0-9]{0,15}$`).MatchString(s.Revision) {
		return errors.New("maintenance_recovery_invalid")
	}
	for _, p := range []*string{s.PlanID, s.ApprovalID, s.ApplyAttempt} {
		if p != nil && !uuidPattern.MatchString(*p) {
			return errors.New("maintenance_recovery_invalid")
		}
	}
	for _, p := range []*string{s.ProviderSHA, s.PlanSHA} {
		if p != nil && !digestPattern.MatchString(*p) {
			return errors.New("maintenance_recovery_invalid")
		}
	}
	return nil
}
func (a RecoveryAudit) Validate() error {
	return a.ValidateForBinding(nil)
}
func (a RecoveryAudit) ValidateForBinding(binding *actioncontract.Binding) error {
	if !uuidPattern.MatchString(a.BootID) || a.Integrity != "clean" || a.ProcessRunning {
		return errors.New("maintenance_recovery_invalid")
	}
	if binding != nil {
		return validateActionAudit(a.Facts)
	}
	raw, err := json.Marshal(a.Facts)
	var facts map[string]string
	if err != nil || controlplane.Decode(raw, &facts) != nil || len(facts) == 0 || len(facts) > 16 {
		return errors.New("maintenance_recovery_invalid")
	}
	for key, v := range facts {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`).MatchString(key) || !digestPattern.MatchString(v) {
			return errors.New("maintenance_recovery_invalid")
		}
	}
	return nil
}
func sameValue(a, b any) bool {
	one, e := recoveryDigest(a)
	two, f := recoveryDigest(b)
	return e == nil && f == nil && one == two
}
func (r RecoveryReply) Validate() error {
	_, e := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if r.Schema != r.Scope.Schema() || r.Scope.Validate() != nil || r.RecoveryID != r.Scope.RecoveryID || e != nil {
		return errors.New("maintenance_recovery_invalid")
	}
	if r.State != "issued" && r.State != "accepted" && r.State != "finalized" {
		return errors.New("maintenance_recovery_invalid")
	}
	if (r.State == "issued") != (r.AcceptanceSHA == nil) || (r.State == "finalized") != (r.FinalizationSHA != nil) {
		return errors.New("maintenance_recovery_invalid")
	}
	for _, p := range []*string{r.AcceptanceSHA, r.FinalizationSHA} {
		if p != nil && !digestPattern.MatchString(*p) {
			return errors.New("maintenance_recovery_invalid")
		}
	}
	return nil
}
func RecoveryPost(client *controlplane.Client) RecoveryTransport {
	return func(ctx context.Context, token, mode string, input any) (RecoveryReply, error) {
		if mode != "describe" && mode != "accept" && mode != "finalize" {
			return RecoveryReply{}, errors.New("maintenance_recovery_invalid")
		}
		var reply RecoveryReply
		raw, err := json.Marshal(input)
		if err != nil {
			return RecoveryReply{}, err
		}
		var header struct {
			Schema int `json:"schema_version"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return RecoveryReply{}, errors.New("maintenance_recovery_invalid")
		}
		if header.Schema == 2 {
			ctx = context.WithValue(ctx, controlplane.SkillActionsFeatureContext{}, true)
		}
		_, e := client.Post(ctx, "/api/v1/maintenance-recovery/"+mode, token, input, false, func(raw []byte) error {
			if e := controlplane.Decode(raw, &reply); e != nil {
				return e
			}
			return reply.Validate()
		}, responseLimit(header.Schema), responseLimit(header.Schema))
		return reply, e
	}
}
