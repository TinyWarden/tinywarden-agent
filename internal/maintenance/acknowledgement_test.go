package maintenance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

func acknowledgedFixture(t *testing.T) (Coordinator, *Journal, *countingProvider) {
	t.Helper()
	c, j, p, _ := coordinatorFixture(t)
	j.Phase = "terminal"
	j.Result = &Result{Effect: "indexes_changed", LocalVerified: true, Facts: json.RawMessage(`{}`)}
	// Exact crash checkpoint: Forget's acknowledgement is fsynced, fence remains.
	j.Acknowledged = true
	if e := Save(c.Base, *j); e != nil {
		t.Fatal(e)
	}
	c.Call = func(_ context.Context, r Request) (Reply, error) {
		return Reply{Schema: 1, HostID: r.HostID, AgentID: r.AgentID, Generation: r.Generation,
			Capability: Capability, ServerAt: time.Now().UTC().Format(time.RFC3339Nano), AckSequence: r.Sequence}, nil
	}
	return c, j, p
}

func TestAcknowledgedCompletionResumesFenceReleaseWithoutExecution(t *testing.T) {
	for _, cleared := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_ack_fsync", true: "after_fence_removal"}[cleared], func(t *testing.T) {
			c, j, p := acknowledgedFixture(t)
			before, _ := readPrivate(journalPath(c.Base), MaxControl)
			if cleared {
				if e := hostmutation.ClearFence(c.Base, j.Intent.Fence()); e != nil {
					t.Fatal(e)
				}
			}
			for n := 0; n < 2; n++ {
				found, e := c.Discover(context.Background())
				if e != nil || found != nil {
					t.Fatal("completion cleanup failed", e)
				}
			}
			after, _ := readPrivate(journalPath(c.Base), MaxControl)
			if string(before) != string(after) || p.prepared+p.fetch+p.apply+p.recover != 0 {
				t.Fatal("completion evidence changed or provider executed")
			}
			if e := hostmutation.AssertIdle(c.Base); e != nil {
				t.Fatal("updater remains fenced", e)
			}
			if _, e := Begin(c.Base, j.Intent); e == nil {
				t.Fatal("acknowledged intent replayed")
			}
			next := j.Intent
			next.ID = "99999999-9999-4999-8999-999999999999"
			if _, e := Begin(c.Base, next); e != nil {
				t.Fatal("new job blocked after completion", e)
			}
			if _, e := readPrivate(filepath.Join(c.Base, "updater", "private", "maintenance-history", j.Intent.ID+".json"), MaxControl); e != nil {
				t.Fatal("completed receipt not retained", e)
			}
		})
	}
}

func TestAcknowledgedCleanupRefusesUntrustedOrUnsettledState(t *testing.T) {
	for _, kind := range []string{"scope", "foreign_fence", "corrupt_fence", "corrupt_journal", "uncertain", "pending_recovery"} {
		t.Run(kind, func(t *testing.T) {
			c, j, p := acknowledgedFixture(t)
			switch kind {
			case "scope":
				c.Scope.Generation++
			case "foreign_fence":
				fence := j.Intent.Fence()
				fence.Generation++
				raw, _ := json.Marshal(fence)
				if e := hostmutation.PrivateWrite(fencePathForTest(c.Base), raw); e != nil {
					t.Fatal(e)
				}
			case "corrupt_fence":
				if e := hostmutation.PrivateWrite(fencePathForTest(c.Base), []byte(`{`)); e != nil {
					t.Fatal(e)
				}
			case "corrupt_journal":
				if e := os.WriteFile(journalPath(c.Base), []byte(`{`), 0600); e != nil {
					t.Fatal(e)
				}
			case "uncertain":
				j.Acknowledged, j.Attention = false, true
				j.Result = &Result{Effect: "unknown", ProcessRunning: true, Facts: json.RawMessage(`{}`)}
				if e := Save(c.Base, *j); e != nil {
					t.Fatal(e)
				}
			case "pending_recovery":
				// A malformed outbox must block too; it never becomes an idle state.
				if e := hostmutation.PrivateWrite(recoveryPath(c.Base), []byte(`{`)); e != nil {
					t.Fatal(e)
				}
			}
			_, e := c.Discover(context.Background())
			if kind != "uncertain" && e == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if hostmutation.AssertIdle(c.Base) == nil || p.prepared+p.fetch+p.apply+p.recover != 0 {
				t.Fatal("unsafe fence cleared or provider executed")
			}
		})
	}
}

func fencePathForTest(base string) string {
	return filepath.Join(base, "updater", "private", "maintenance-active.json")
}

func TestAcknowledgedCleanupCannotBypassPendingRecoveryRelease(t *testing.T) {
	c, p, wire, token, _ := recoveryFixture(t)
	r, e := c.prepare(context.Background(), token)
	if e != nil {
		t.Fatal(e)
	}
	reply, e := wire.call(context.Background(), token, "accept", r.Acceptance)
	if e != nil {
		t.Fatal(e)
	}
	r.Accepted, r.State = &reply, "release_intent"
	r.Finalization = &RecoveryFinalization{1, r.Acceptance.Scope.RecoveryID, *reply.AcceptanceSHA,
		LocalRelease{r.Acceptance.Scope.JobID, r.Acceptance.LocalAttempt, true, r.Acceptance.Audit}}
	if e = SaveRecovery(c.Base, *r); e != nil {
		t.Fatal(e)
	}
	if e = closeRecoveredJournal(c.Base, r); e != nil {
		t.Fatal(e)
	}
	j, e := Load(c.Base)
	if e != nil {
		t.Fatal(e)
	}
	if resumeAcknowledged(c.Base, j) == nil || hostmutation.AssertIdle(c.Base) == nil {
		t.Fatal("normal acknowledgement bypassed ordered recovery release")
	}
	if e = c.Reconcile(context.Background(), ""); e != nil {
		t.Fatal("ordered recovery cannot resume", e)
	}
	assertNoExecution(t, p)
}
