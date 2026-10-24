package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

type Transport func(context.Context, Request) (Reply, error)
type Coordinator struct {
	Base     string
	Scope    Scope
	Registry Registry
	Call     Transport
}

func instant() (*BootInstant, error) {
	boot, ms, e := BootClock()
	if e != nil {
		return nil, e
	}
	return &BootInstant{boot, ms}, nil
}
func (c Coordinator) request(j *Journal, kind string, data any) (Request, error) {
	raw, e := json.Marshal(data)
	if e != nil {
		return Request{}, e
	}
	return Request{1, c.Scope.HostID, c.Scope.AgentID, c.Scope.Generation, &j.Intent.ID, j.Sequence + 1, kind, raw}, nil
}
func (c Coordinator) queue(j *Journal, kind string, data any) error {
	if len(j.Pending) > 0 || j.Sequence >= MaxEvents {
		return errors.New("maintenance_sequence_invalid")
	}
	request, e := c.request(j, kind, data)
	if e != nil {
		return e
	}
	j.Pending, e = json.Marshal(request)
	if e != nil {
		return e
	}
	j.PendingOrigin, e = instant()
	if e != nil {
		return e
	}
	j.Unsent = ""
	return Save(c.Base, *j)
}
func (c Coordinator) poll(ctx context.Context, j *Journal) (Reply, error) {
	request := Request{Schema: 1, HostID: c.Scope.HostID, AgentID: c.Scope.AgentID, Generation: c.Scope.Generation, Kind: "poll", Data: json.RawMessage(`{}`)}
	if j != nil {
		request.JobID = &j.Intent.ID
	}
	reply, e := c.Call(ctx, request)
	if e != nil {
		return Reply{}, e
	}
	return reply, reply.Validate(request, c.Registry)
}
func (c Coordinator) Discover(ctx context.Context) (*Journal, error) {
	j, e := Load(c.Base)
	if e == nil {
		if j.Intent.Scope != c.Scope {
			return nil, errors.New("maintenance_scope_changed")
		}
		if !j.Acknowledged {
			return j, nil
		}
		if e = resumeAcknowledged(c.Base, j); e != nil {
			return nil, e
		}
	} else if !isMissing(e) {
		return nil, e
	}
	reply, e := c.poll(ctx, nil)
	if e != nil || reply.Job == nil {
		return nil, e
	}
	d := reply.Job
	// Never create a new root journal for an already issued/terminal transaction.
	if d.Phase != "preparing" || d.Grant != nil || d.Attention || d.CancelRequested {
		return nil, errors.New("maintenance_reconciliation_required")
	}
	return Begin(c.Base, Intent{d.ID, d.Scope, d.Action, d.ProviderVersion, d.ExpiresAt})
}

// exchange acknowledges and consumes a start grant in ONE atomic journal write.
// Retries retain the first request's boot clock; an aged reply cannot renew it.
func (c Coordinator) exchange(ctx context.Context, j *Journal) (Reply, error) {
	var request Request
	if controlplane.Decode(j.Pending, &request) != nil || j.PendingOrigin == nil {
		return Reply{}, errors.New("maintenance_journal_invalid")
	}
	sent, e := instant()
	if e != nil {
		return Reply{}, e
	}
	reply, e := c.Call(ctx, request)
	if e != nil {
		return Reply{}, e
	}
	if e = reply.Validate(request, c.Registry); e != nil {
		return Reply{}, e
	}
	origin := *j.PendingOrigin
	j.Sequence = request.Sequence
	j.Pending = nil
	j.PendingOrigin = nil
	if reply.Job != nil {
		if e = c.bind(j, *reply.Job); e != nil {
			return Reply{}, e
		}
		if request.Kind == "ready" && reply.Job.Grant != nil {
			start, e := StartFromReply(reply, sent.BootID, sent.Milliseconds)
			if e == nil && origin.BootID != start.BootID {
				e = errors.New("maintenance_start_expired")
			}
			if e == nil && start.NotAfterMS > origin.Milliseconds+GrantMilliseconds {
				start.NotAfterMS = origin.Milliseconds + GrantMilliseconds
			}
			if e == nil {
				boot, now, clockError := BootClock()
				e = clockError
				if e == nil {
					e = ValidateStart(start, j.Intent.ID, boot, now)
				}
			}
			if e == nil {
				e = planFresh(j)
			}
			if e != nil {
				reason := "maintenance_start_expired"
				j.Phase = "terminal"
				j.Unsent = "start_declined"
				j.Result = &Result{Effect: "indexes_changed", Reason: &reason, Facts: json.RawMessage(`{}`)}
				if e = Save(c.Base, *j); e != nil {
					return Reply{}, e
				}
				if e = c.queue(j, "start_declined", struct{}{}); e != nil {
					return Reply{}, e
				}
			} else if e = StartApply(c.Base, j, start); e != nil {
				return Reply{}, e
			}
			return reply, nil
		}
	}
	switch request.Kind {
	case "prepared":
		j.PrepareNotified = true
	case "plan_chunk":
		var data struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(request.Data, &data)
		n, e := chunkLength(data.Content)
		if e != nil {
			return Reply{}, e
		}
		j.UploadedBytes += n
	case "downloading":
		j.Phase = "downloading"
	}
	if e = Save(c.Base, *j); e != nil {
		return Reply{}, e
	}
	return reply, nil
}
func (c Coordinator) bind(j *Journal, d Delivery) error {
	if d.ID != j.Intent.ID || d.Scope != j.Intent.Scope || d.Action != j.Intent.Action || d.ProviderVersion != j.Intent.ProviderVersion {
		return errors.New("maintenance_scope_changed")
	}
	if j.PlanSHA != nil && d.ProviderSHA != nil && *j.PlanSHA != *d.ProviderSHA {
		return errors.New("maintenance_plan_changed")
	}
	for _, pair := range []struct {
		local  **string
		remote *string
	}{{&j.PlanID, d.PlanID}, {&j.EnvelopeSHA, d.PlanSHA}, {&j.ApprovalID, d.ApprovalID}} {
		if pair.remote == nil {
			continue
		}
		if *pair.local != nil && **pair.local != *pair.remote {
			return errors.New("maintenance_plan_changed")
		}
		*pair.local = pair.remote
	}
	return nil
}
func planFresh(j *Journal) error {
	now, e := instant()
	if e != nil {
		return e
	}
	if j.PreparedAt == nil || now.BootID != j.PreparedAt.BootID || now.Milliseconds < j.PreparedAt.Milliseconds || now.Milliseconds-j.PreparedAt.Milliseconds >= int64(30*time.Minute/time.Millisecond) {
		return errors.New("maintenance_plan_expired")
	}
	return nil
}
