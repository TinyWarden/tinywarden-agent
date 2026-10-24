package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

func fixtureIntent() Intent {
	return Intent{ID: "11111111-1111-4111-8111-111111111111", Scope: Scope{"22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", 1}, Action: "test.inspect.v1", ProviderVersion: 1, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
}
func TestGrantClockAndScope(t *testing.T) {
	boot, now, e := BootClock()
	if e != nil {
		t.Fatal(e)
	}
	s := Start{fixtureIntent().ID, "44444444-4444-4444-8444-444444444444", boot, now + 30000}
	if ValidateStart(s, s.JobID, boot, now) != nil {
		t.Fatal("valid start refused")
	}
	for _, changed := range []Start{{s.JobID, s.AttemptID, boot, now}, {s.JobID, s.AttemptID, "55555555-5555-4555-8555-555555555555", now + 30000}, {s.JobID, s.AttemptID, boot, now + 60001}} {
		if ValidateStart(changed, s.JobID, boot, now) == nil {
			t.Fatal("stale/wrong-boot/oversized grant admitted")
		}
	}
}
func TestJournalNeverReplaysApplyAndPreservesAcknowledgedReceipt(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on designated disposable host")
	}
	base := t.TempDir()
	if e := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); e != nil {
		t.Fatal(e)
	}
	guard, e := hostmutation.Lock(base)
	if e != nil {
		t.Fatal(e)
	}
	defer guard.Close()
	j, e := Begin(base, fixtureIntent())
	if e != nil {
		t.Fatal(e)
	}
	j.Phase = "preparing"
	if e = Save(base, *j); e != nil {
		t.Fatal(e)
	}
	plan := []byte(`{"action":"test.inspect.v1"}`)
	sum := sha256.Sum256(plan)
	if e = SavePlan(base, j, plan, hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	j.Phase = "downloading"
	approval := "66666666-6666-4666-8666-666666666666"
	j.ApprovalID = &approval
	j.PlanID = &approval
	boot, now, e := BootClock()
	if e != nil {
		t.Fatal(e)
	}
	start := Start{j.Intent.ID, "44444444-4444-4444-8444-444444444444", boot, now + 60000}
	if e = StartApply(base, j, start); e != nil {
		t.Fatal(e)
	}
	recovered, e := Load(base)
	if e != nil {
		t.Fatal(e)
	}
	if !Interrupted(recovered) || StartApply(base, recovered, start) == nil {
		t.Fatal("crashed apply was replayed")
	}
	if Forget(base, recovered) == nil || hostmutation.AssertIdle(base) == nil {
		t.Fatal("uncertain work lost fencing")
	}
	recovered.Phase = "terminal"
	recovered.Attention = true
	recovered.Result = &Result{Effect: "unknown"}
	if e = Save(base, *recovered); e != nil {
		t.Fatal(e)
	}
	if Forget(base, recovered) == nil {
		t.Fatal("uncertain terminal cleared")
	}
	recovered.Attention = false
	recovered.Result = &Result{Effect: "packages_changed", LocalVerified: true}
	if e = Save(base, *recovered); e != nil {
		t.Fatal(e)
	}
	if _, e = Begin(base, fixtureIntent()); e == nil {
		t.Fatal("unacknowledged terminal replaced")
	}
	if e = Forget(base, recovered); e != nil {
		t.Fatal(e)
	}
	next := fixtureIntent()
	next.ID = "77777777-7777-4777-8777-777777777777"
	if _, e = Begin(base, next); e != nil {
		t.Fatal(e)
	}
	if _, e = readPrivate(filepath.Join(base, "updater", "private", "maintenance-history", j.Intent.ID+".json"), MaxControl); e != nil {
		t.Fatal(e)
	}
}

type fakeProvider struct{}

func (fakeProvider) Prepare(context.Context) (Prepared, error) {
	return Prepared{NoChanges: true, Effect: "none"}, nil
}
func (fakeProvider) Fetch(context.Context, string) error   { return nil }
func (fakeProvider) Apply(string, Start) (Result, error)   { return Result{LocalVerified: true}, nil }
func (fakeProvider) Recover(string, Start) (Result, error) { return Result{LocalVerified: true}, nil }
func TestFutureProviderUsesGenericRegistry(t *testing.T) {
	r := Registry{{ID: "test.inspect.v1", Version: 1, New: func() Provider { return fakeProvider{} }}}
	definition, e := r.Resolve("test.inspect.v1", 1)
	if e != nil {
		t.Fatal(e)
	}
	result, e := definition.New().Prepare(context.Background())
	if e != nil || !result.NoChanges {
		t.Fatal("fake provider required an APT branch")
	}
	if _, e = r.Resolve("test.inspect.v1", 2); e == nil {
		t.Fatal("unknown provider version admitted")
	}
}
