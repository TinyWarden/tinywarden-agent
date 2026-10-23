package installation

import (
	"context"
	"encoding/json"
	"errors"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func remoteFixture(t *testing.T) (agentrelease.RemoteUpdate, agentrelease.Manifest) {
	t.Helper()
	b, e := os.ReadFile("testdata/remote-releases.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures struct {
		Releases []struct {
			Manifest  string
			Signature string
		}
	}
	if e = json.Unmarshal(b, &fixtures); e != nil {
		t.Fatal(e)
	}
	r := fixtures.Releases[1]
	m, e := agentrelease.Verify([]byte(r.Manifest), r.Signature)
	if e != nil {
		t.Fatal(e)
	}
	return agentrelease.RemoteUpdate{ID: "11111111-1111-4111-8111-111111111111", State: "queued", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Candidate: agentrelease.Candidate{Manifest: r.Manifest, Signature: r.Signature}}, m
}
func TestRemoteReplyScopeAndSignedTarget(t *testing.T) {
	job, _ := remoteFixture(t)
	at := time.Now()
	request := agentrelease.RemoteUpdateRequest{Schema: 1, HostID: "host", AgentID: "agent", Generation: 4, RequestID: &job.ID, State: "poll"}
	good := agentrelease.RemoteUpdateReply{Schema: 1, HostID: "host", AgentID: "agent", Generation: 4, Capability: agentrelease.RemoteUpdateCapability, Request: &job}
	if e := remoteReply(good, request, at); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"schema", "host", "agent", "generation", "capability", "missing", "id", "state", "expiry", "signature"} {
		t.Run(name, func(t *testing.T) {
			reply := good
			v := job
			reply.Request = &v
			switch name {
			case "schema":
				reply.Schema = 2
			case "host":
				reply.HostID = "other"
			case "agent":
				reply.AgentID = "other"
			case "generation":
				reply.Generation++
			case "capability":
				reply.Capability = "old"
			case "missing":
				reply.Request = nil
			case "id":
				v.ID = "22222222-2222-4222-8222-222222222222"
			case "state":
				v.State = "execute"
			case "expiry":
				v.ExpiresAt = at.Add(-time.Second).Format(time.RFC3339Nano)
			case "signature":
				v.Candidate.Manifest += " "
			}
			if remoteReply(reply, request, at) == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	job.State = "failed"
	job.ExpiresAt = at.Add(-time.Hour).Format(time.RFC3339Nano)
	if e := remoteReply(good, request, at); e != nil {
		t.Fatal("expired terminal result cannot be acknowledged", e)
	}
}
func TestRemoteRequestJournalAndRecovery(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("disposable root-owned intent fixture")
	}
	for _, scenario := range []string{"install", "lost_ack", "confirmation_retry", "interrupted", "scope_change", "expired_confirmation", "expired_queued", "same_version", "broken_installation"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			if e := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); e != nil {
				t.Fatal(e)
			}
			job, target := remoteFixture(t)
			scope := agentstate.State{HostID: "host", AgentID: "agent", Generation: 1}
			installed := target
			installed.Version = "0.0.16"
			installs, confirms, acks := 0, 0, 0
			failAck := scenario == "lost_ack"
			confirmFailure := scenario == "confirmation_retry"
			work := remoteWork{
				call: func(id *string, state string, problem *string) (*agentrelease.RemoteUpdate, error) {
					if state == "succeeded" || state == "failed" {
						acks++
						if failAck {
							failAck = false
							return nil, errors.New("network down")
						}
						job.State = state
						job.Error = problem
					}
					if state == "installing" {
						job.State = state
					}
					if state == "confirming" {
						job.State = state
					}
					copy := job
					return &copy, nil
				},
				current: func() (agentrelease.Manifest, error) {
					if scenario == "broken_installation" {
						return installed, errors.New("receipt mismatch")
					}
					return installed, nil
				},
				update: func(automatic, checkOnly bool, r *agentrelease.RemoteUpdate) error {
					if checkOnly {
						confirms++
						p, e := pendingTransaction(base)
						if e != nil {
							return e
						}
						p.Phase = "completed"
						return record(base, *p)
					}
					if automatic || r == nil || r.Candidate != job.Candidate {
						t.Fatal("remote target or manual authority lost")
					}
					installs++
					if scenario == "interrupted" {
						if e := record(base, transaction{Target: target.Version, Previous: installed.Version, Phase: "switching"}); e != nil {
							t.Fatal(e)
						}
						return errors.New("interrupted")
					}
					installed = target
					if confirmFailure {
						if e := record(base, transaction{Target: target.Version, Previous: "0.0.16", Phase: "awaiting_confirmation"}); e != nil {
							t.Fatal(e)
						}
						return errors.New("waiting")
					}
					return nil
				},
				confirm: func(m agentrelease.Manifest) error { confirms++; return nil },
			}
			if scenario == "same_version" {
				installed = target
			}
			if scenario == "scope_change" {
				old := remoteIntent{HostID: "host", AgentID: "agent", Generation: 2, Request: job, State: "installing"}
				if e := saveIntent(base, old); e != nil {
					t.Fatal(e)
				}
				if e := record(base, transaction{Target: target.Version, Previous: "0.0.16", Phase: "staged"}); e != nil {
					t.Fatal(e)
				}
				if runRemote(base, scope, work) == nil {
					t.Fatal("obsolete intent executed")
				}
				if installs != 0 {
					t.Fatal("obsolete install")
				}
				return
			}
			if scenario == "expired_confirmation" || scenario == "expired_queued" {
				if e := saveIntent(base, remoteIntent{HostID: scope.HostID, AgentID: scope.AgentID, Generation: 1, Request: job, State: "installing"}); e != nil {
					t.Fatal(e)
				}
				job.State = "failed"
				if scenario == "expired_confirmation" {
					installed = target
					if e := record(base, transaction{Target: target.Version, Previous: "0.0.16", Phase: "awaiting_confirmation"}); e != nil {
						t.Fatal(e)
					}
				}
				if e := runRemote(base, scope, work); e != nil {
					t.Fatal(e)
				}
				if installs != 0 {
					t.Fatal("expired request installed")
				}
				if scenario == "expired_confirmation" && confirms != 1 {
					t.Fatal("installed process not confirmed")
				}
				return
			}
			first := runRemote(base, scope, work)
			if scenario == "lost_ack" || scenario == "confirmation_retry" {
				if first == nil {
					t.Fatal("uncertain result hidden")
				}
				confirmFailure = false
				if e := runRemote(base, scope, work); e != nil {
					t.Fatal(e)
				}
				if installs != 1 {
					t.Fatal("uncertain work reinstalled", installs)
				}
			} else if first != nil {
				t.Fatal(first)
			}
			if scenario == "broken_installation" && (installs != 0 || job.State != "failed" || job.Error == nil || *job.Error != "update_repair_required") {
				t.Fatal("broken installation retried")
			}
			if scenario == "interrupted" {
				if job.State != "failed" || job.Error == nil || *job.Error != "update_repair_required" {
					t.Fatal("unsafe transaction not fenced")
				}
				if pending, e := pendingTransaction(base); e != nil || pending == nil {
					t.Fatal("recovery journal lost")
				}
			}
			if scenario == "same_version" && (installs != 0 || confirms != 1) {
				t.Fatal("same release was reinstalled")
			}
			if scenario == "confirmation_retry" {
				if pending, e := pendingTransaction(base); e != nil || pending != nil {
					t.Fatal("confirmation journal not completed")
				}
			}
			if acks < 1 {
				t.Fatal("completion not acknowledged")
			}
			if _, e := os.Stat(intentPath(base)); !os.IsNotExist(e) {
				t.Fatal("acknowledged intent retained")
			}
		})
	}
}

func TestPollArgumentConflicts(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("disposable root argument fixture")
	}
	copy := SetupMessages{PollArguments: "invalid poll options", Arguments: "invalid setup options"}
	for _, args := range [][]string{{"check", "--poll"}, {"update", "--poll", "--automatic"}, {"update", "--poll", "--archive", "/not-opened"}, {"update", "--poll", "--setup-code-stdin"}} {
		handled, e := Dispatch(context.Background(), args, io.Discard, io.Discard, ReconnectMessages{}, copy)
		want := copy.PollArguments
		if args[len(args)-1] == "--setup-code-stdin" {
			want = copy.Arguments
		}
		if !handled || e == nil || e.Error() != want {
			t.Fatal("invalid poll reached root operation", args, e)
		}
	}
}
