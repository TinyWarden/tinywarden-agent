package maintenance

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

func TestRecoveryLostReceiptRetriesPreserveOriginalFailure(t *testing.T) {
	for _, lost := range []string{"accept", "finalize"} {
		t.Run(lost, func(t *testing.T) {
			c, p, w, token, old := recoveryFixture(t)
			w.lose = lost
			if c.Reconcile(context.Background(), token) == nil {
				t.Fatal("expected lost reply")
			}
			if e := c.Reconcile(context.Background(), ""); e != nil {
				explainRecovery(t, c.Base)
				t.Fatal(e)
			}
			j, e := Load(c.Base)
			if e != nil {
				t.Fatal(e)
			}
			if !sameValue(j.Result, old.Result) || j.Result.LocalVerified || !j.Acknowledged || j.Recovery == nil {
				t.Fatal("recovery rewrote original failure")
			}
			r, e := LoadRecovery(c.Base)
			if e != nil || r.State != "finalized" || r.Token != "" {
				t.Fatal("secret/outbox not finalized", e)
			}
			if e = hostmutation.AssertIdle(c.Base); e != nil {
				t.Fatal(e)
			}
			if _, e = Begin(c.Base, old.Intent); e == nil {
				t.Fatal("old execution replayed")
			}
			if p.inspections != 2 {
				t.Fatalf("inspected %d times", p.inspections)
			}
			assertNoExecution(t, p)
		})
	}
}
func TestRecoveryReleaseCrashBoundaries(t *testing.T) {
	for _, point := range []string{"before_close", "after_close", "after_clear"} {
		t.Run(point, func(t *testing.T) {
			c, p, w, token, _ := recoveryFixture(t)
			r, e := c.prepare(context.Background(), token)
			if e != nil {
				t.Fatal(e)
			}
			reply, e := w.call(context.Background(), token, "accept", r.Acceptance)
			if e != nil {
				t.Fatal(e)
			}
			r.Accepted = &reply
			r.State = "release_intent"
			r.Finalization = &RecoveryFinalization{1, r.Acceptance.Scope.RecoveryID, *reply.AcceptanceSHA, LocalRelease{r.Acceptance.Scope.JobID, r.Acceptance.LocalAttempt, true, r.Acceptance.Audit}}
			if e = SaveRecovery(c.Base, *r); e != nil {
				t.Fatal(e)
			}
			if point != "before_close" {
				if e = closeRecoveredJournal(c.Base, r); e != nil {
					t.Fatal(e)
				}
			}
			if point == "after_clear" {
				if e = hostmutation.ClearFence(c.Base, fixtureIntent().Fence()); e != nil {
					t.Fatal(e)
				}
			}
			if e = c.Reconcile(context.Background(), ""); e != nil {
				t.Fatal(e)
			}
			count := 2
			if point == "after_clear" {
				count = 1
			}
			if p.inspections != count {
				t.Fatal("incorrect historical receipt/audit restart", p.inspections)
			}
			r, e = LoadRecovery(c.Base)
			if e != nil || r.State != "finalized" {
				t.Fatal(e)
			}
			assertNoExecution(t, p)
		})
	}
}
func TestRecoveryReadOnlyDeliveryNeverReleasesOrInspects(t *testing.T) {
	c, p, w, token, _ := recoveryFixture(t)
	r, e := c.prepare(context.Background(), token)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.RetryRecoveryDelivery(context.Background()); e != nil {
		t.Fatal(e)
	}
	if p.inspections != 1 || w.accept != "" {
		t.Fatal("poller initiated recovery")
	}
	reply, e := w.call(context.Background(), token, "accept", r.Acceptance)
	if e != nil {
		t.Fatal(e)
	}
	r.Accepted = &reply
	r.State = "accepted"
	if e = SaveRecovery(c.Base, *r); e != nil {
		t.Logf("pre-save state=%s accepted_valid=%v scope=%v hash=%v local=%v", r.State, r.Accepted.Validate(), sameValue(r.Accepted.Scope, r.Acceptance.Scope), r.Accepted.AcceptanceSHA, sameValue(r.Acceptance.LocalAttempt, r.Acceptance.Scope.ApplyAttempt))
		t.Fatal(e)
	}
	if e = c.RetryRecoveryDelivery(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = hostmutation.AssertIdle(c.Base); e == nil {
		t.Fatal("delivery path released fence")
	}
	w.lose = "finalize"
	if c.Reconcile(context.Background(), "") == nil {
		t.Fatal("expected lost reply")
	}
	before := p.inspections
	if e = c.RetryRecoveryDelivery(context.Background()); e != nil {
		t.Fatal(e)
	}
	if p.inspections != before {
		t.Fatal("receipt delivery ran a new native inspection")
	}
	assertNoExecution(t, p)
}
func TestRecoveryRefusesDirtyOrUnknownIdentityAndPreservesFence(t *testing.T) {
	c, p, w, token, _ := recoveryFixture(t)
	p.dirty = true
	if c.Reconcile(context.Background(), token) == nil {
		t.Fatal("dirty system reconciled")
	}
	if w.accept != "" || hostmutation.AssertIdle(c.Base) == nil {
		t.Fatal("dirty state lost fence")
	}
	p.dirty = false
	c.Verify = func(RecoveryScope) error { return errors.New("lingering worker or changed identity") }
	if c.Reconcile(context.Background(), token) == nil {
		t.Fatal("unknown worker state admitted")
	}
	if w.accept != "" || hostmutation.AssertIdle(c.Base) == nil {
		t.Fatal("unknown identity lost fence")
	}
	assertNoExecution(t, p)
}
func TestRecoveryValidOrphanFenceCannotReconstructApply(t *testing.T) {
	c, p, _, token, _ := recoveryFixture(t)
	if e := os.Remove(journalPath(c.Base)); e != nil {
		t.Fatal(e)
	}
	if e := c.Reconcile(context.Background(), token); e != nil {
		t.Fatal(e)
	}
	j, e := Load(c.Base)
	if e != nil || j.ApplyAttempt != nil || j.Start != nil || j.Result.LocalVerified || !j.Acknowledged {
		t.Fatal("orphan created execution proof", e)
	}
	assertNoExecution(t, p)
}
func TestRecoveryCorruptJournalAndUnrecordedMissingFenceStayBlocked(t *testing.T) {
	t.Run("corrupt", func(t *testing.T) {
		c, p, w, token, _ := recoveryFixture(t)
		if e := hostmutation.PrivateWrite(journalPath(c.Base), []byte(`{"broken":true}`)); e != nil {
			t.Fatal(e)
		}
		if c.Reconcile(context.Background(), token) == nil || w.accept != "" || hostmutation.AssertIdle(c.Base) == nil {
			t.Fatal("corrupt identity cleared")
		}
		assertNoExecution(t, p)
	})
	t.Run("missing_fence", func(t *testing.T) {
		c, p, w, token, _ := recoveryFixture(t)
		r, e := c.prepare(context.Background(), token)
		if e != nil {
			t.Fatal(e)
		}
		reply, e := w.call(context.Background(), token, "accept", r.Acceptance)
		if e != nil {
			t.Fatal(e)
		}
		r.Accepted = &reply
		r.State = "accepted"
		if e = SaveRecovery(c.Base, *r); e != nil {
			t.Fatal(e)
		}
		if e = hostmutation.ClearFence(c.Base, fixtureIntent().Fence()); e != nil {
			t.Fatal(e)
		}
		if c.Reconcile(context.Background(), "") == nil {
			t.Fatal("unrecorded release accepted")
		}
		assertNoExecution(t, p)
	})
}

func TestRecoveryPreservesUnuploadedPreparationEvidence(t *testing.T) {
	c, p, w, token, j := recoveryFixture(t)
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	j.ApplyAttempt = nil
	j.PlanSHA = &sha
	j.Phase = "awaiting_approval"
	if e := Save(c.Base, *j); e != nil {
		t.Fatal(e)
	}
	w.reply.Scope.ApplyAttempt = nil
	if e := c.Reconcile(context.Background(), token); e != nil {
		t.Fatal(e)
	}
	saved, e := Load(c.Base)
	if e != nil || saved.PlanSHA == nil || *saved.PlanSHA != sha || saved.ApplyAttempt != nil || saved.Result.LocalVerified {
		t.Fatal("unsent preparation was lost or became authority", e)
	}
	assertNoExecution(t, p)
}

func TestRecoveryReplacementGenerationCannotInheritOldExecution(t *testing.T) {
	c, p, _, _, j := recoveryFixture(t)
	replacement := j.Intent.Scope
	replacement.Generation++
	worker := Coordinator{Base: c.Base, Scope: replacement, Registry: c.Registry}
	if worker.Work(context.Background(), j) == nil {
		t.Fatal("replacement generation inherited execution")
	}
	if hostmutation.AssertIdle(c.Base) == nil {
		t.Fatal("generation change cleared old fence")
	}
	assertNoExecution(t, p)
}
