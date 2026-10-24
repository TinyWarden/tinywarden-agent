package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

type countingProvider struct{ prepared, fetch, apply, recover int }

var fakePlan = json.RawMessage(`{"action":"test.inspect.v1"}`)

func (p *countingProvider) Prepare(context.Context) (Prepared, error) {
	p.prepared++
	sum := sha256.Sum256(fakePlan)
	return Prepared{Plan: fakePlan, Digest: hex.EncodeToString(sum[:]), Effect: "indexes_changed"}, nil
}
func (p *countingProvider) Fetch(context.Context, string) error { p.fetch++; return nil }
func (p *countingProvider) Apply(string, Start) (Result, error) {
	p.apply++
	return Result{Effect: "packages_changed", LocalVerified: true, Facts: json.RawMessage(`{}`)}, nil
}
func (p *countingProvider) Recover(string, Start) (Result, error) {
	p.recover++
	return Result{}, errors.New("incomplete native receipt")
}

type wireServer struct {
	intent    Intent
	phase     string
	result    *Result
	attention bool
	lose      string
	lost      bool
	replies   map[uint64]Reply
	bodies    map[uint64]string
}

func (s *wireServer) call(_ context.Context, r Request) (Reply, error) {
	now := time.Now().UTC()
	plan, approval := "66666666-6666-4666-8666-666666666666", "77777777-7777-4777-8777-777777777777"
	sum := sha256.Sum256(fakePlan)
	digest := hex.EncodeToString(sum[:])
	envelope := digest
	delivery := func() *Delivery {
		return &Delivery{ID: s.intent.ID, Scope: s.intent.Scope, Action: s.intent.Action, ProviderVersion: 1, Phase: s.phase, ExpiresAt: s.intent.ExpiresAt, Result: s.result, Attention: s.attention, ProviderSHA: &digest, PlanSHA: &envelope, PlanID: &plan, ApprovalID: &approval}
	}
	reply := Reply{1, r.HostID, r.AgentID, r.Generation, Capability, now.Format(time.RFC3339Nano), r.Sequence, nil}
	if r.Kind != "poll" {
		raw, _ := json.Marshal(r)
		if previous, ok := s.replies[r.Sequence]; ok {
			if s.bodies[r.Sequence] != string(raw) {
				return Reply{}, errors.New("changed retry")
			}
			previous.ServerAt = reply.ServerAt
			return previous, nil
		}
		switch r.Kind {
		case "prepared":
		case "plan_chunk":
			s.phase = "approved"
		case "downloading":
			s.phase = "downloading"
		case "ready":
			s.phase = "applying"
		case "verifying":
			var v struct {
				Result Result `json:"result"`
			}
			_ = json.Unmarshal(r.Data, &v)
			s.result = &v.Result
			s.phase = "succeeded"
		case "failed":
			var v struct {
				Result Result `json:"result"`
			}
			_ = json.Unmarshal(r.Data, &v)
			s.result = &v.Result
			s.phase = "failed"
			s.attention = s.result.Effect == "unknown"
		case "start_declined":
			reason := "maintenance_start_expired"
			s.result = &Result{Effect: "indexes_changed", Reason: &reason, Facts: json.RawMessage(`{}`)}
			s.phase = "expired"
		default:
			return Reply{}, errors.New("unexpected event")
		}
		reply.Job = delivery()
		if r.Kind == "ready" {
			reply.Job.Grant = &Grant{"88888888-8888-4888-8888-888888888888", now.Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano)}
		}
		s.replies[r.Sequence] = reply
		s.bodies[r.Sequence] = string(raw)
		if s.lose == r.Kind && !s.lost {
			s.lost = true
			return Reply{}, controlplane.InterruptedResponse(errors.New("lost acknowledgement"))
		}
	} else {
		reply.Job = delivery()
	}
	return reply, nil
}
func coordinatorFixture(t *testing.T) (Coordinator, *Journal, *countingProvider, *wireServer) {
	t.Helper()
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
	t.Cleanup(func() { guard.Close() })
	intent := fixtureIntent()
	j, e := Begin(base, intent)
	if e != nil {
		t.Fatal(e)
	}
	p := &countingProvider{}
	server := &wireServer{intent: intent, phase: "preparing", replies: map[uint64]Reply{}, bodies: map[uint64]string{}}
	return Coordinator{base, intent.Scope, Registry{{ID: intent.Action, Version: 1, Approval: true, New: func() Provider { return p }}}, server.call}, j, p, server
}
func TestCoordinatorLostGrantReplyAppliesOnlyOnce(t *testing.T) {
	c, j, p, s := coordinatorFixture(t)
	s.lose = "ready"
	if e := c.Work(context.Background(), j); e == nil {
		t.Fatal("lost response did not preserve retry")
	}
	disk, e := Load(c.Base)
	if e != nil || disk.ApplyAttempt != nil || len(disk.Pending) == 0 {
		t.Fatal("unacknowledged grant boundary lost", e)
	}
	if e = c.Work(context.Background(), disk); e != nil {
		t.Fatal(e)
	}
	if p.prepared != 1 || p.fetch != 1 || p.apply != 1 || p.recover != 0 {
		t.Fatalf("provider replay: %+v", p)
	}
	disk, e = Load(c.Base)
	if e != nil || !disk.Acknowledged {
		t.Fatal("terminal acknowledgement not durable", e)
	}
	if _, e = Begin(c.Base, j.Intent); e == nil {
		t.Fatal("completed job replayed")
	}
}
func TestCoordinatorPersistedApplyNeverRunsAgain(t *testing.T) {
	c, j, p, s := coordinatorFixture(t)
	s.lose = "ready"
	if e := c.Work(context.Background(), j); e == nil {
		t.Fatal("expected lost response")
	}
	disk, e := Load(c.Base)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.exchange(context.Background(), disk); e != nil {
		t.Fatal(e)
	}
	if disk.Phase != "applying" || disk.ApplyAttempt == nil {
		t.Fatal("apply not durably consumed")
	}
	// Simulate death after intent fsync but before native spawn.
	disk, e = Load(c.Base)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Work(context.Background(), disk); e != nil {
		t.Fatal(e)
	}
	if p.apply != 0 || p.recover != 1 || !disk.Attention || disk.Acknowledged {
		t.Fatal("uncertain start was replayed or released")
	}
}
func TestCoordinatorFirstBootOriginCannotRenewGrant(t *testing.T) {
	c, j, p, s := coordinatorFixture(t)
	s.lose = "ready"
	if e := c.Work(context.Background(), j); e == nil {
		t.Fatal("expected lost response")
	}
	disk, e := Load(c.Base)
	if e != nil {
		t.Fatal(e)
	}
	// A later HTTP exchange still cannot create a fresh60-second local window.
	disk.PendingOrigin.BootID = "99999999-9999-4999-8999-999999999999"
	if e = Save(c.Base, *disk); e != nil {
		t.Fatal(e)
	}
	if e = c.Work(context.Background(), disk); e != nil {
		t.Fatal(e)
	}
	if p.apply != 0 || p.recover != 0 || !disk.Acknowledged {
		t.Fatal("stale-boot grant executed or remained ambiguous")
	}
}
func TestCoordinatorFirstRequestAgeCannotRenewGrant(t *testing.T) {
	c, j, p, s := coordinatorFixture(t)
	s.lose = "ready"
	if e := c.Work(context.Background(), j); e == nil {
		t.Fatal("expected lost response")
	}
	disk, e := Load(c.Base)
	if e != nil {
		t.Fatal(e)
	}
	_, now, e := BootClock()
	if e != nil || now <= GrantMilliseconds {
		t.Fatal("fixture boot duration unavailable")
	}
	disk.PendingOrigin.Milliseconds = now - GrantMilliseconds - 1
	if e = Save(c.Base, *disk); e != nil {
		t.Fatal(e)
	}
	if e = c.Work(context.Background(), disk); e != nil {
		t.Fatal(e)
	}
	if p.apply != 0 || p.recover != 0 || !disk.Acknowledged {
		t.Fatal("first request age renewed")
	}
}
