package maintenance

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
)

func TestSchema2CoordinatorLostGrantStillExecutesOnce(t *testing.T) {
	coordinator, journal, provider, wire := coordinatorFixture(t)
	i := actionIntent(t)
	i.ID = journal.Intent.ID
	i.Scope = journal.Intent.Scope
	i.ExpiresAt = journal.Intent.ExpiresAt
	i.Action = journal.Intent.Action
	i.Binding.ActionID = i.Action
	journal.Intent = i
	journal.Version = 2
	wire.intent = i
	if err := Save(coordinator.Base, *journal); err != nil {
		t.Fatal(err)
	}
	coordinator.Schema = 2
	coordinator.Registry = Registry{{Resolve: func(intent Intent) (Definition, error) {
		return Definition{ID: intent.Action, Version: intent.ProviderVersion, Approval: true, PrepareEffect: "none", New: func() Provider { return provider }}, nil
	}}}
	coordinator.Call = func(ctx context.Context, request Request) (Reply, error) {
		reply, err := wire.call(ctx, request)
		if err != nil {
			return reply, err
		}
		reply.Schema = 2
		reply.Capability = actioncontract.Capability
		if reply.Job != nil {
			reply.Job.Binding = i.Binding
			reply.Job.InputJSON = i.InputJSON
			reply.Job.SettingsJSON = i.SettingsJSON
			reply.Job.Archive = i.Archive
		}
		return reply, nil
	}
	wire.lose = "ready"
	if err := coordinator.Work(context.Background(), journal); err == nil {
		t.Fatal("lost acknowledgement not exercised")
	}
	loaded, err := Load(coordinator.Base)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Work(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	if provider.apply != 1 {
		t.Fatal("schema2 execution replayed", provider.apply)
	}
}

func actionIntent(t *testing.T) Intent {
	t.Helper()
	raw, err := os.ReadFile("../../sdk/python/tests/fixtures/skill-actions-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Invocation struct {
			Binding      actioncontract.Binding `json:"binding"`
			Scope        Scope                  `json:"scope"`
			JobID        string                 `json:"job_id"`
			InputJSON    string                 `json:"input_json"`
			SettingsJSON string                 `json:"settings_json"`
		} `json:"invocation"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	v := fixture.Invocation
	return Intent{ID: v.JobID, Scope: v.Scope, Action: v.Binding.ActionID, ProviderVersion: v.Binding.ActionVersion,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Binding: &v.Binding,
		InputJSON: v.InputJSON, SettingsJSON: v.SettingsJSON, Archive: &actioncontract.Archive{SHA256: strings.Repeat("d", 64), Size: 123}}
}

func TestActionIntentBindsExactInputAndSettings(t *testing.T) {
	i := actionIntent(t)
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	i.InputJSON = "{ }"
	if i.Validate() == nil {
		t.Fatal("changed input bytes accepted")
	}
	i = actionIntent(t)
	i.Binding.PermissionRevision = "0"
	if i.Validate() == nil {
		t.Fatal("invalid permission revision accepted")
	}
	i = actionIntent(t)
	i.Binding = nil
	if i.Validate() == nil {
		t.Fatal("new invocation fields accepted as legacy intent")
	}
}

func TestActionProtocolUsesClosedSchemaAndFullBinding(t *testing.T) {
	i := actionIntent(t)
	seen := 0
	r := Registry{{Resolve: func(received Intent) (Definition, error) {
		seen++
		if !actioncontract.Equal(received.Binding, i.Binding) {
			t.Fatal("resolver lost binding")
		}
		return Definition{ID: i.Action, Version: i.ProviderVersion, PrepareEffect: "none", New: func() Provider { return fakeProvider{} }}, nil
	}}}
	d := &Delivery{ID: i.ID, Scope: i.Scope, Action: i.Action, ProviderVersion: i.ProviderVersion,
		ExpiresAt: i.ExpiresAt, Phase: "preparing", Binding: i.Binding, InputJSON: i.InputJSON, SettingsJSON: i.SettingsJSON, Archive: i.Archive}
	request := Request{Schema: 2, HostID: i.Scope.HostID, AgentID: i.Scope.AgentID, Generation: i.Scope.Generation, Kind: "poll"}
	reply := Reply{Schema: 2, Capability: actioncontract.Capability, HostID: request.HostID, AgentID: request.AgentID,
		Generation: request.Generation, ServerAt: time.Now().UTC().Format(time.RFC3339Nano), Job: d}
	if err := reply.Validate(request, r); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatal("resolver not called")
	}
	request.JobID = &i.ID
	request.Binding = i.Binding
	changed := *i.Binding
	changed.DescriptorSHA = strings.Repeat("c", 64)
	d.Binding = &changed
	if reply.Validate(request, r) == nil {
		t.Fatal("changed action definition accepted")
	}
	d.Binding = i.Binding
	request.Schema = 1
	reply.Schema = 1
	reply.Capability = Capability
	if reply.Validate(request, r) == nil {
		t.Fatal("new binding passed through legacy schema")
	}
}

func TestActionDeliveryCannotChangeArgumentsExpiryOrPermission(t *testing.T) {
	i := actionIntent(t)
	j := &Journal{Version: 2, Intent: i}
	d := Delivery{ID: i.ID, Scope: i.Scope, Action: i.Action, ProviderVersion: i.ProviderVersion,
		ExpiresAt: i.ExpiresAt, Binding: i.Binding, InputJSON: i.InputJSON, SettingsJSON: i.SettingsJSON, Archive: i.Archive}
	if err := (Coordinator{}).bind(j, d); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*Delivery){
		func(v *Delivery) { v.InputJSON = "{ }" }, func(v *Delivery) { v.SettingsJSON = "{ }" },
		func(v *Delivery) { v.ExpiresAt = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano) },
		func(v *Delivery) { value := *v.Binding; value.PermissionRevision = "2"; v.Binding = &value },
	}
	for _, mutate := range mutations {
		value := d
		mutate(&value)
		if (Coordinator{}).bind(j, value) == nil {
			t.Fatal("changed immutable delivery accepted")
		}
	}
}

func TestLaterFailureRetainsCompletedPreparationEffects(t *testing.T) {
	for _, tc := range []struct {
		name, phase, preparation, incoming, expected string
		prepared, applyAttempt                       bool
	}{
		{"fetch refusal", "downloading", "changed", "none", "changed", true, false},
		{"pre-start apply refusal", "applying", "changed", "none", "changed", true, true},
		{"uncertain apply", "applying", "changed", "unknown", "unknown", true, true},
		{"preparation refusal", "preparing", "changed", "none", "none", false, false},
		{"read-only preparation", "downloading", "none", "none", "none", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, j, _, _ := coordinatorFixture(t)
			c.Schema = 2
			i := actionIntent(t)
			i.ID, i.Scope = j.Intent.ID, j.Intent.Scope
			j.Intent, j.Version, j.Phase = i, 2, tc.phase
			if tc.prepared {
				var err error
				j.PreparedAt, err = instant()
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.applyAttempt {
				attempt := "00000005-1111-4111-8111-111111111111"
				j.ApplyAttempt = &attempt
			}
			c.Registry = Registry{{Resolve: func(Intent) (Definition, error) {
				return Definition{PrepareEffect: tc.preparation}, nil
			}}}
			reason := "maintenance_action_failed"
			facts := json.RawMessage(`{"outcome":"not_started"}`)
			if err := c.failed(j, Result{Effect: tc.incoming, Reason: &reason, Facts: facts}, tc.preparation); err != nil {
				t.Fatal(err)
			}
			saved, err := Load(c.Base)
			if err != nil || saved.Result == nil || saved.Result.Effect != tc.expected || string(saved.Result.Facts) != string(facts) {
				t.Fatal("incorrect durable cumulative failure", saved, err)
			}
			var event Request
			if err = json.Unmarshal(saved.Pending, &event); err != nil {
				t.Fatal(err)
			}
			var data struct {
				Result Result `json:"result"`
			}
			if err = json.Unmarshal(event.Data, &data); err != nil || data.Result.Effect != tc.expected {
				t.Fatal("wire result lost earlier effects", string(event.Data), err)
			}
		})
	}
}
