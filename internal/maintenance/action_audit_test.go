package maintenance

import (
	"context"
	"encoding/json"
	"testing"
)

type typedInspectionProvider struct {
	countingProvider
	inspections int
}

func (p *typedInspectionProvider) InspectAction(_ context.Context, commit func(json.RawMessage) error) error {
	p.inspections++
	return commit(json.RawMessage(`{"complete":true,"ratio":1.0,"label":"é😀<>&"}`))
}

func TestActionRecoveryUsesTypedFactsWithoutExecutingAgain(t *testing.T) {
	coordinator, _, wire, token, journal := recoveryFixture(t)
	intent := actionIntent(t)
	intent.ID = journal.Intent.ID
	intent.Scope = journal.Intent.Scope
	intent.Action = journal.Intent.Action
	intent.ExpiresAt = journal.Intent.ExpiresAt
	intent.Binding.ActionID = intent.Action
	journal.Intent = intent
	journal.Version = 2
	if err := Save(coordinator.Base, *journal); err != nil {
		t.Fatal(err)
	}
	wire.reply.Schema = 2
	scope := &wire.reply.Scope
	scope.Binding = intent.Binding
	scope.InputJSON = intent.InputJSON
	scope.SettingsJSON = intent.SettingsJSON
	scope.Archive = intent.Archive
	provider := &typedInspectionProvider{}
	coordinator.Registry = Registry{{Resolve: func(i Intent) (Definition, error) {
		return Definition{ID: i.Action, Version: i.ProviderVersion, PrepareEffect: "none", New: func() Provider { return provider }}, nil
	}}}
	if err := coordinator.Reconcile(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if provider.inspections != 2 || provider.prepared != 0 || provider.fetch != 0 || provider.apply != 0 || provider.recover != 0 {
		t.Fatal("recovery executed or lost ordered inspection")
	}
	record, err := LoadRecovery(coordinator.Base)
	if err != nil || record.Schema != 2 || record.State != "finalized" {
		t.Fatal("typed recovery not durable", err)
	}
}

func TestActionAuditBindsTypedFactsAndRetainsLegacyRules(t *testing.T) {
	facts, err := NewActionAuditFacts(json.RawMessage(`{"complete":true,"ratio":1.0,"name":"é"}`))
	if err != nil {
		t.Fatal(err)
	}
	audit := RecoveryAudit{BootID: "00000006-1111-4111-8111-111111111111", Integrity: "clean", Facts: facts}
	intent := actionIntent(t)
	if err = audit.ValidateForBinding(intent.Binding); err != nil {
		t.Fatal(err)
	}
	if audit.Validate() == nil {
		t.Fatal("new facts accepted in legacy audit")
	}
	changed := facts
	changed.JSON = `{"complete":false}`
	audit.Facts = changed
	if audit.ValidateForBinding(intent.Binding) == nil {
		t.Fatal("changed fact bytes accepted")
	}
	if _, err = NewActionAuditFacts(json.RawMessage(`{"complete":true,"complete":false}`)); err == nil {
		t.Fatal("duplicate fact accepted")
	}
}

func TestRecoveryScopeBindsPackagePermissionAndArguments(t *testing.T) {
	i := actionIntent(t)
	scope := RecoveryScope{HostID: i.Scope.HostID, AgentID: i.Scope.AgentID, Generation: i.Scope.Generation,
		RecoveryID: "00000008-1111-4111-8111-111111111111", JobID: i.ID, OperationID: "00000009-1111-4111-8111-111111111111", Action: i.Action, ProviderVersion: i.ProviderVersion,
		ExpiresAt: i.ExpiresAt, Revision: "1", Binding: i.Binding, InputJSON: i.InputJSON, SettingsJSON: i.SettingsJSON, Archive: i.Archive}
	if err := scope.Validate(); err != nil {
		t.Fatal(err)
	}
	journal := Journal{Version: 2, Intent: i, Phase: "preparing"}
	if err := matchRecoveryJournal(journal, scope); err != nil {
		t.Fatal(err)
	}
	changed := *scope.Binding
	changed.PermissionRevision = "2"
	scope.Binding = &changed
	if matchRecoveryJournal(journal, scope) == nil {
		t.Fatal("recovery changed permission identity")
	}
}
