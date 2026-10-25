package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

type Request struct {
	Schema     int                     `json:"schema_version"`
	HostID     string                  `json:"host_id"`
	AgentID    string                  `json:"agent_id"`
	Generation uint64                  `json:"generation"`
	JobID      *string                 `json:"job_id"`
	Sequence   uint64                  `json:"sequence"`
	Kind       string                  `json:"kind"`
	Data       json.RawMessage         `json:"data"`
	Binding    *actioncontract.Binding `json:"binding,omitempty"`
}
type Grant struct {
	AttemptID string `json:"attempt_id"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
}
type Delivery struct {
	ID              string                  `json:"id"`
	Scope           Scope                   `json:"scope"`
	Action          string                  `json:"action"`
	ProviderVersion int                     `json:"provider_version"`
	Phase           string                  `json:"phase"`
	Attention       bool                    `json:"attention"`
	CancelRequested bool                    `json:"cancel_requested"`
	ExpiresAt       string                  `json:"expires_at"`
	PlanID          *string                 `json:"plan_id"`
	ProviderSHA     *string                 `json:"provider_sha256"`
	PlanSHA         *string                 `json:"plan_sha256"`
	ApprovalID      *string                 `json:"approval_id"`
	Grant           *Grant                  `json:"grant"`
	Result          *Result                 `json:"result"`
	Binding         *actioncontract.Binding `json:"binding,omitempty"`
	InputJSON       string                  `json:"input_json,omitempty"`
	SettingsJSON    string                  `json:"settings_json,omitempty"`
	Archive         *actioncontract.Archive `json:"archive,omitempty"`
}
type Reply struct {
	Schema          int                    `json:"schema_version"`
	HostID          string                 `json:"host_id"`
	AgentID         string                 `json:"agent_id"`
	Generation      uint64                 `json:"generation"`
	Capability      string                 `json:"capability"`
	ServerAt        string                 `json:"server_at"`
	AckSequence     uint64                 `json:"ack_sequence"`
	Job             *Delivery              `json:"job"`
	Probes          []actioncontract.Probe `json:"probes,omitempty"`
	NextProbeCursor *string                `json:"next_probe_cursor,omitempty"`
}

func (r Reply) Validate(request Request, registry Registry) error {
	at, e := time.Parse(time.RFC3339Nano, r.ServerAt)
	capability := Capability
	if request.Schema == 2 {
		capability = actioncontract.Capability
	}
	if (request.Schema != 1 && request.Schema != 2) || r.Schema != request.Schema || r.Capability != capability || r.ScopeValue() != request.ScopeValue() || r.AckSequence != request.Sequence || e != nil || at.IsZero() {
		return errors.New("maintenance_reply_invalid")
	}
	if r.Schema == 1 && (len(r.Probes) > 0 || r.NextProbeCursor != nil) || len(r.Probes) > 32 || r.NextProbeCursor != nil && !uuidPattern.MatchString(*r.NextProbeCursor) {
		return errors.New("action_probe_invalid")
	}
	seen := map[string]bool{}
	for _, probe := range r.Probes {
		if !probe.Valid() || seen[probe.GrantID] {
			return errors.New("action_probe_invalid")
		}
		seen[probe.GrantID] = true
	}
	if r.Job == nil {
		return nil
	}
	j := r.Job
	if j.Scope != r.ScopeValue() || !uuidPattern.MatchString(j.ID) || request.JobID != nil && j.ID != *request.JobID {
		return errors.New("maintenance_scope_invalid")
	}
	if (r.Schema == 2) != (j.Binding != nil) || !actioncontract.Equal(request.Binding, j.Binding) && request.JobID != nil {
		return errors.New("action_binding_invalid")
	}
	if j.Intent().Validate() != nil {
		return errors.New("maintenance_reply_invalid")
	}
	if _, e := registry.ResolveIntent(j.Intent()); e != nil {
		return e
	}
	for _, v := range []*string{j.ProviderSHA, j.PlanSHA} {
		if v != nil && !digestPattern.MatchString(*v) {
			return errors.New("maintenance_reply_invalid")
		}
	}
	for _, v := range []*string{j.PlanID, j.ApprovalID} {
		if v != nil && !uuidPattern.MatchString(*v) {
			return errors.New("maintenance_reply_invalid")
		}
	}
	allowed := map[string]bool{"queued": true, "preparing": true, "awaiting_approval": true, "approved": true, "downloading": true, "applying": true, "verifying": true, "succeeded": true, "no_changes": true, "blocked": true, "failed": true, "expired": true, "cancelled": true}
	if !allowed[j.Phase] {
		return errors.New("maintenance_reply_invalid")
	}
	expiry, _ := time.Parse(time.RFC3339Nano, j.ExpiresAt)
	if !terminal(j.Phase) && !j.CancelRequested && !expiry.After(at) {
		return errors.New("maintenance_expired")
	}
	if j.Grant != nil {
		issued, e1 := time.Parse(time.RFC3339Nano, j.Grant.IssuedAt)
		expires, e2 := time.Parse(time.RFC3339Nano, j.Grant.ExpiresAt)
		jobExpiry, _ := time.Parse(time.RFC3339Nano, j.ExpiresAt)
		if issued.After(at) || expires.After(jobExpiry) || j.Phase != "applying" || j.Attention || j.CancelRequested || j.PlanID == nil || j.ApprovalID == nil || j.ProviderSHA == nil || j.PlanSHA == nil || !uuidPattern.MatchString(j.Grant.AttemptID) || e1 != nil || e2 != nil || !expires.After(issued) || expires.Sub(issued) > 60*time.Second {
			return errors.New("maintenance_reply_invalid")
		}
	}
	return nil
}

// Post keeps the established TLS, credential, redirect and retry-error boundary.
// Existing endpoints retain their existing limits; only this lane admits 64KiB.
func Post(ctx context.Context, client *controlplane.Client, credential string, request Request, registry Registry) (Reply, error) {
	if request.Schema == 2 {
		ctx = context.WithValue(ctx, controlplane.SkillActionsFeatureContext{}, true)
	}
	var reply Reply
	wire, e := client.Post(ctx, "/api/v1/agent/maintenance-jobs", credential, request, false, func(raw []byte) error {
		if e := controlplane.Decode(raw, &reply); e != nil {
			return e
		}
		return reply.Validate(request, registry)
	}, MaxControl, responseLimit(request.Schema))
	if e != nil {
		return Reply{}, e
	}
	if wire == nil {
		return Reply{}, errors.New("maintenance_reply_invalid")
	}
	return reply, nil
}

func responseLimit(schema int) int {
	if schema == 2 {
		return MaxDelivery
	}
	return MaxControl
}

func (d Delivery) Intent() Intent {
	return Intent{ID: d.ID, Scope: d.Scope, Action: d.Action, ProviderVersion: d.ProviderVersion, ExpiresAt: d.ExpiresAt, Binding: d.Binding, InputJSON: d.InputJSON, SettingsJSON: d.SettingsJSON, Archive: d.Archive}
}

// A reply never renews a grant. Age is derived from fresh server time and the
// monotonic instant BEFORE the request, charging the entire round trip locally.
func StartFromReply(reply Reply, sentBoot string, sentMS int64) (Start, error) {
	if reply.Job == nil || reply.Job.Grant == nil {
		return Start{}, errors.New("maintenance_start_invalid")
	}
	server, e1 := time.Parse(time.RFC3339Nano, reply.ServerAt)
	expires, e2 := time.Parse(time.RFC3339Nano, reply.Job.Grant.ExpiresAt)
	remaining := expires.Sub(server).Milliseconds()
	if e1 != nil || e2 != nil || remaining <= 0 || remaining > GrantMilliseconds {
		return Start{}, errors.New("maintenance_start_expired")
	}
	start := Start{reply.Job.ID, reply.Job.Grant.AttemptID, sentBoot, sentMS + remaining}
	boot, now, e := BootClock()
	if e != nil {
		return Start{}, e
	}
	if e = ValidateStart(start, reply.Job.ID, boot, now); e != nil {
		return Start{}, e
	}
	return start, nil
}

func (r Request) ScopeValue() Scope { return Scope{r.HostID, r.AgentID, r.Generation} }
func (r Reply) ScopeValue() Scope   { return Scope{r.HostID, r.AgentID, r.Generation} }
