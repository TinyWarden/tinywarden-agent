package installation

import (
	"errors"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
	"os"
	"time"
)

// The request coordinator owns durable intent; the installed updater owns root
// validation, signed staging, atomic switching and process confirmation.
type remoteWork struct {
	call    func(*string, string, *string) (*agentrelease.RemoteUpdate, error)
	current func() (agentrelease.Manifest, error)
	update  func(bool, bool, *agentrelease.RemoteUpdate) error
	confirm func(agentrelease.Manifest) error
}

func runRemote(base string, s agentstate.State, work remoteWork) error {
	var intent remoteIntent
	var id *string
	data, e := agentrelease.ReadPublic(intentPath(base), 32768)
	if e == nil {
		if agentrelease.Decode(data, &intent) != nil || !agentrelease.UUIDPattern.MatchString(intent.Request.ID) || !remoteActive(intent.State) && intent.State != "succeeded" && intent.State != "failed" {
			return errors.New("update_request_invalid")
		}
		if intent.HostID != s.HostID || intent.AgentID != s.AgentID || intent.Generation != s.Generation {
			pending, e := pendingTransaction(base)
			if e != nil {
				return e
			}
			if pending != nil {
				return errors.New("update_repair_required")
			}
			if e = os.Remove(intentPath(base)); e != nil {
				return e
			}
		} else {
			id = &intent.Request.ID
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	r, e := work.call(id, "poll", nil)
	if e != nil {
		return e
	}
	if r == nil {
		return work.update(true, false, nil)
	}
	if !remoteActive(r.State) {
		pending, err := pendingTransaction(base)
		if err != nil {
			return err
		}
		if pending != nil && pending.Phase == "awaiting_confirmation" {
			if err = work.update(false, true, nil); err != nil {
				return err
			}
		}
		return os.Remove(intentPath(base))
	}
	if id != nil && intent.Request.Candidate != r.Candidate {
		return errors.New("update_scope_changed")
	}
	if id == nil {
		intent = remoteIntent{HostID: s.HostID, AgentID: s.AgentID, Generation: s.Generation, Request: *r, State: "installing"}
		if e = saveIntent(base, intent); e != nil {
			return e
		}
	}
	if intent.State == "succeeded" || intent.State == "failed" {
		return acknowledgeRemote(base, work, intent)
	}
	if _, e = work.call(&r.ID, "installing", nil); e != nil {
		return e
	}
	installed, e := work.current()
	if e != nil {
		// A broken signed installation needs local repair, not endless delivery.
		problem := "update_repair_required"
		intent.State, intent.Error = "failed", &problem
		if e = saveIntent(base, intent); e != nil {
			return e
		}
		_ = status(base, agentrelease.UpdateStatus{Outcome: "failed", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Error: "update_failed"})
		return acknowledgeRemote(base, work, intent)
	}
	target, e := agentrelease.Verify([]byte(r.Candidate.Manifest), r.Candidate.Signature)
	if e != nil {
		return e
	}
	comparison, e := agentrelease.Compare(target.Version, installed.Version)
	if e != nil {
		return e
	}
	switch {
	case comparison < 0:
		e = errors.New("update_scope_changed")
	case comparison == 0:
		// The same target may already be installed after a lost acknowledgement.
		pending, readError := pendingTransaction(base)
		if readError != nil {
			return readError
		}
		if pending != nil && (pending.Phase != "awaiting_confirmation" || pending.Target != installed.Version) {
			e = errors.New("update_repair_required")
		} else {
			e = work.confirm(installed)
			if e == nil && pending != nil {
				pending.Phase = "completed"
				e = record(base, *pending)
			}
			if e == nil {
				e = cleanup(base, installed.Version)
			}
		}
	default:
		e = work.update(false, false, r)
	}
	if e == nil {
		intent.State = "succeeded"
		intent.Error = nil
	} else {
		pending, readError := pendingTransaction(base)
		if readError != nil {
			return readError
		}
		if pending != nil && pending.Phase == "awaiting_confirmation" {
			intent.State = "confirming"
			if saveError := saveIntent(base, intent); saveError != nil {
				return saveError
			}
			_, _ = work.call(&r.ID, "confirming", nil)
			return e
		}
		problem := "update_failed"
		if pending != nil {
			problem = "update_repair_required"
		}
		if comparison < 0 {
			problem = "update_scope_changed"
		}
		intent.State = "failed"
		intent.Error = &problem
		_ = status(base, agentrelease.UpdateStatus{Outcome: "failed", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Target: target.Version, Error: "update_failed"})
	}
	if e = saveIntent(base, intent); e != nil {
		return e
	}
	return acknowledgeRemote(base, work, intent)
}
func acknowledgeRemote(base string, work remoteWork, intent remoteIntent) error {
	r, e := work.call(&intent.Request.ID, intent.State, intent.Error)
	if e != nil {
		return e
	}
	if r == nil || remoteActive(r.State) {
		return errors.New("update_confirmation_required")
	}
	return os.Remove(intentPath(base))
}
