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

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

type RecoveryScope struct {
	HostID          string  `json:"host_id"`
	AgentID         string  `json:"agent_id"`
	Generation      uint64  `json:"generation"`
	RecoveryID      string  `json:"recovery_id"`
	JobID           string  `json:"job_id"`
	OperationID     string  `json:"operation_id"`
	Action          string  `json:"action"`
	ProviderVersion int     `json:"provider_version"`
	ExpiresAt       string  `json:"job_expires_at"`
	Revision        string  `json:"revision"`
	PlanID          *string `json:"plan_id"`
	ProviderSHA     *string `json:"provider_sha256"`
	PlanSHA         *string `json:"plan_sha256"`
	ApprovalID      *string `json:"approval_id"`
	ApplyAttempt    *string `json:"apply_attempt"`
}

func (s RecoveryScope) ScopeValue() Scope { return Scope{s.HostID, s.AgentID, s.Generation} }

type RecoveryAudit struct {
	BootID         string            `json:"boot_id"`
	Integrity      string            `json:"integrity"`
	ProcessRunning bool              `json:"process_running"`
	Facts          map[string]string `json:"facts"`
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
	raw, e = json.Marshal(value)
	if e != nil || len(raw) > MaxControl {
		return "", errors.New("maintenance_recovery_invalid")
	}
	// Closed recovery fields are ASCII; encoding/json's sorted map keys match the app canonical format.
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (s RecoveryScope) Validate() error {
	if (Intent{s.JobID, s.ScopeValue(), s.Action, s.ProviderVersion, s.ExpiresAt}).Validate() != nil || !uuidPattern.MatchString(s.RecoveryID) || !uuidPattern.MatchString(s.OperationID) || !regexp.MustCompile(`^[1-9][0-9]{0,15}$`).MatchString(s.Revision) {
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
	if !uuidPattern.MatchString(a.BootID) || a.Integrity != "clean" || a.ProcessRunning || len(a.Facts) == 0 || len(a.Facts) > 16 {
		return errors.New("maintenance_recovery_invalid")
	}
	for key, v := range a.Facts {
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
	if r.Schema != 1 || r.Scope.Validate() != nil || r.RecoveryID != r.Scope.RecoveryID || e != nil {
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
		_, e := client.Post(ctx, "/api/v1/maintenance-recovery/"+mode, token, input, false, func(raw []byte) error {
			if e := controlplane.Decode(raw, &reply); e != nil {
				return e
			}
			return reply.Validate()
		}, MaxControl, MaxControl)
		return reply, e
	}
}
