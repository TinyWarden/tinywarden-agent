package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

type Journal struct {
	Recovery        *RecoveryClosure `json:"recovery,omitempty"`
	Acknowledged    bool             `json:"acknowledged"`
	Version         int              `json:"schema_version"`
	Intent          Intent           `json:"intent"`
	Phase           string           `json:"phase"`
	PlanSHA         *string          `json:"plan_sha256"`
	EnvelopeSHA     *string          `json:"envelope_sha256"`
	ApprovalID      *string          `json:"approval_id"`
	PlanID          *string          `json:"plan_id"`
	ApplyAttempt    *string          `json:"apply_attempt"`
	Start           *Start           `json:"start"`
	Sequence        uint64           `json:"sequence"`
	Pending         json.RawMessage  `json:"pending"`
	PendingOrigin   *BootInstant     `json:"pending_origin"`
	Unsent          string           `json:"unsent"`
	PreparedAt      *BootInstant     `json:"prepared_at"`
	PrepareNotified bool             `json:"prepare_notified"`
	FetchStarted    bool             `json:"fetch_started"`
	Downloaded      bool             `json:"downloaded"`
	UploadedBytes   int              `json:"uploaded_bytes"`
	Attention       bool             `json:"attention"`
	Result          *Result          `json:"result"`
}
type BootInstant struct {
	BootID       string `json:"boot_id"`
	Milliseconds int64  `json:"milliseconds"`
}

func journalPath(base string) string {
	return filepath.Join(base, "updater", "private", "maintenance-job.json")
}
func planPath(base string) string {
	return filepath.Join(base, "updater", "private", "maintenance-plan.json")
}
func readPrivate(path string, limit int) ([]byte, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Size() > int64(limit) {
		return nil, errors.New("maintenance_journal_invalid")
	}
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if e != nil || len(raw) > limit {
		return nil, errors.New("maintenance_journal_invalid")
	}
	return raw, nil
}
func (j Journal) Validate() error {
	if j.Recovery != nil && (!j.Recovery.Valid() || !j.Acknowledged || j.Phase != "terminal" || j.Attention) {
		return errors.New("maintenance_journal_invalid")
	}
	expectedVersion := 1
	if j.Intent.Binding != nil {
		expectedVersion = 2
	}
	if j.Version != expectedVersion || j.Intent.Validate() != nil || j.Sequence > MaxEvents || j.UploadedBytes < 0 || j.UploadedBytes > MaxPlan {
		return errors.New("maintenance_journal_invalid")
	}
	if j.PlanSHA != nil && !digestPattern.MatchString(*j.PlanSHA) {
		return errors.New("maintenance_journal_invalid")
	}
	if j.EnvelopeSHA != nil && !digestPattern.MatchString(*j.EnvelopeSHA) {
		return errors.New("maintenance_journal_invalid")
	}
	if j.Unsent != "" && j.Unsent != "failed" && j.Unsent != "verifying" && j.Unsent != "completed" && j.Unsent != "cancelled" && j.Unsent != "start_declined" {
		return errors.New("maintenance_journal_invalid")
	}
	for _, instant := range []*BootInstant{j.PendingOrigin, j.PreparedAt} {
		if instant != nil && (!uuidPattern.MatchString(instant.BootID) || instant.Milliseconds < 0) {
			return errors.New("maintenance_journal_invalid")
		}
	}
	if len(j.Pending) > 0 {
		var request Request
		if controlplane.Decode(j.Pending, &request) != nil || request.Schema != expectedVersion || !actioncontract.Equal(request.Binding, j.Intent.Binding) || request.JobID == nil || *request.JobID != j.Intent.ID || request.ScopeValue() != j.Intent.Scope || request.Sequence != j.Sequence+1 || j.PendingOrigin == nil {
			return errors.New("maintenance_journal_invalid")
		}
	} else if j.PendingOrigin != nil {
		return errors.New("maintenance_journal_invalid")
	}
	if j.ApplyAttempt != nil && !uuidPattern.MatchString(*j.ApplyAttempt) {
		return errors.New("maintenance_journal_invalid")
	}
	phases := map[string]bool{"queued": true, "preparing": true, "awaiting_approval": true, "approved": true, "downloading": true, "applying": true, "verifying": true, "terminal": true}
	if !phases[j.Phase] || (j.Phase == "applying" || j.Phase == "verifying") && j.ApplyAttempt == nil {
		return errors.New("maintenance_journal_invalid")
	}
	if j.Start != nil && (j.ApplyAttempt == nil || j.Start.AttemptID != *j.ApplyAttempt || j.Start.JobID != j.Intent.ID) {
		return errors.New("maintenance_journal_invalid")
	}
	if (j.ApprovalID != nil && !uuidPattern.MatchString(*j.ApprovalID)) || (j.PlanID != nil && !uuidPattern.MatchString(*j.PlanID)) {
		return errors.New("maintenance_journal_invalid")
	}
	if j.Phase == "terminal" && (j.Result == nil || j.ApplyAttempt != nil && !j.Result.LocalVerified && !j.Attention && j.Recovery == nil) {
		return errors.New("maintenance_journal_invalid")
	}
	if j.Acknowledged && (j.Phase != "terminal" || j.Attention || j.Result == nil || j.Result.ProcessRunning && j.Recovery == nil) {
		return errors.New("maintenance_journal_invalid")
	}
	return nil
}
func Load(base string) (*Journal, error) {
	raw, e := readPrivate(journalPath(base), MaxJournal)
	if e != nil {
		return nil, e
	}
	var j Journal
	if controlplane.Decode(raw, &j) != nil {
		return nil, errors.New("maintenance_journal_invalid")
	}
	if bytes.Equal(bytes.TrimSpace(j.Pending), []byte("null")) {
		j.Pending = nil
	}
	if j.Validate() != nil {
		return nil, errors.New("maintenance_journal_invalid")
	}
	if j.Version == 1 && len(raw) > MaxControl {
		return nil, errors.New("maintenance_journal_invalid")
	}
	return &j, nil
}
func Save(base string, j Journal) error {
	if j.Validate() != nil {
		return errors.New("maintenance_journal_invalid")
	}
	raw, e := json.Marshal(j)
	if e != nil || len(raw) > MaxJournal || j.Version == 1 && len(raw) > MaxControl {
		return errors.New("maintenance_journal_invalid")
	}
	return hostmutation.PrivateWrite(journalPath(base), raw)
}

// Begin records the fence first; an interrupted initialization remains visibly blocked.
func Begin(base string, i Intent) (*Journal, error) {
	if i.Validate() != nil {
		return nil, errors.New("maintenance_scope_invalid")
	}
	if _, e := readPrivate(filepath.Join(base, "updater", "private", "maintenance-history", i.ID+".json"), MaxJournal); e == nil || !os.IsNotExist(e) {
		return nil, errors.New("maintenance_replayed_intent")
	}
	if old, e := Load(base); e == nil {
		if old.Intent.ID == i.ID {
			return nil, errors.New("maintenance_replayed_intent")
		}
		if e = archiveAcknowledged(base, *old); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, errors.New("maintenance_reconciliation_required")
	}
	if e := hostmutation.WriteFence(base, i.Fence()); e != nil {
		return nil, e
	}
	version := 1
	if i.Binding != nil {
		version = 2
	}
	j := Journal{Version: version, Intent: i, Phase: "queued"}
	if e := Save(base, j); e != nil {
		return nil, e
	}
	return &j, nil
}
func SavePlan(base string, j *Journal, plan json.RawMessage, digest string) error {
	sum := sha256.Sum256(plan)
	if hex.EncodeToString(sum[:]) != digest {
		return errors.New("maintenance_plan_invalid")
	}
	if len(plan) > MaxPlan || !digestPattern.MatchString(digest) || j.Phase != "preparing" {
		return errors.New("maintenance_plan_invalid")
	}
	if e := hostmutation.PrivateWrite(planPath(base), plan); e != nil {
		return e
	}
	j.PlanSHA = &digest
	j.Phase = "awaiting_approval"
	return Save(base, *j)
}
func ReadPlan(base string) (json.RawMessage, error) { return readPrivate(planPath(base), MaxPlan) }

// StartApply persists the one irreversible attempt before a provider can be invoked.
// A crash after this write requires reconciliation, even if spawn never happened.
func StartApply(base string, j *Journal, start Start) error {
	if j.Phase != "downloading" || j.ApplyAttempt != nil || j.PlanSHA == nil || j.ApprovalID == nil || j.PlanID == nil || start.JobID != j.Intent.ID || !uuidPattern.MatchString(start.AttemptID) {
		return errors.New("maintenance_start_invalid")
	}
	boot, now, err := BootClock()
	if err != nil {
		return err
	}
	if err = ValidateStart(start, j.Intent.ID, boot, now); err != nil {
		return err
	}
	j.ApplyAttempt = &start.AttemptID
	j.Start = &start
	j.Phase = "applying"
	return Save(base, *j)
}
func Interrupted(j *Journal) bool {
	return j.Phase == "preparing" || j.Phase == "downloading" || j.Phase == "applying"
}

// Forget follows an authenticated terminal acknowledgement and durable local result.
func Forget(base string, j *Journal) error {
	if j.Phase != "terminal" || j.Attention || j.Result == nil || j.Result.ProcessRunning {
		return errors.New("maintenance_reconciliation_required")
	}
	j.Acknowledged = true
	if e := Save(base, *j); e != nil {
		return e
	}
	if e := hostmutation.ClearFence(base, j.Intent.Fence()); e != nil {
		return e
	}
	// Keep the completed root receipt. A later Begin archives it before replacement.
	return nil
}

// Discover runs under the common root guard after worker-cgroup exclusion. A
// crash after acknowledgement fsync may leave only its matching fence to remove.
// Recovery closures have a separate ordered release/outbox protocol.
func resumeAcknowledged(base string, j *Journal) error {
	if !j.Acknowledged || j.Validate() != nil {
		return errors.New("maintenance_reconciliation_required")
	}
	if pending, e := PendingRecovery(base); e != nil || pending {
		return errors.New("maintenance_recovery_delivery_pending")
	}
	fence, e := hostmutation.ReadFence(base)
	if e != nil || fence == nil {
		return e
	}
	if j.Recovery != nil || j.Result.ProcessRunning || *fence != j.Intent.Fence() {
		return errors.New("maintenance_reconciliation_required")
	}
	return hostmutation.ClearFence(base, j.Intent.Fence())
}

func archiveAcknowledged(base string, j Journal) error {
	if !j.Acknowledged || j.Phase != "terminal" || j.Attention || j.Result == nil || j.Result.ProcessRunning && j.Recovery == nil {
		return errors.New("maintenance_reconciliation_required")
	}
	fence, e := hostmutation.ReadFence(base)
	if e != nil || fence != nil {
		return errors.New("maintenance_reconciliation_required")
	}
	dir := filepath.Join(base, "updater", "private", "maintenance-history")
	if e = os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	raw, e := readPrivate(journalPath(base), MaxJournal)
	if e != nil {
		return e
	}
	target := filepath.Join(dir, j.Intent.ID+".json")
	if prior, e := readPrivate(target, MaxJournal); e == nil {
		if !bytes.Equal(prior, raw) {
			return errors.New("maintenance_journal_invalid")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if e = hostmutation.PrivateWrite(target, raw); e != nil {
		return e
	}
	if e = cleanupArchivedAction(base, j); e != nil {
		return e
	}
	if e = os.Remove(journalPath(base)); e != nil {
		return e
	}
	parent, e := os.Open(filepath.Dir(journalPath(base)))
	if e != nil {
		return e
	}
	defer parent.Close()
	return parent.Sync()
}
