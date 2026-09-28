package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestLegacyPendingEnrollmentAndReplacementUpgradeBeforeReplay(t *testing.T) {
	for _, replacing := range []bool{false, true} {
		name := "enrollment"
		if replacing {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			pending := testPending(t)
			pending.AgentVersion = "" // Version-1 state written by the 0.0.1 client.
			state := State{Version: 1}
			if replacing {
				old, _ := newCredential()
				state.Credential = old
				state.HostID, _ = randomID()
				state.AgentID, _ = randomID()
				state.Generation = 1
				state.HeartbeatIntervalSeconds = 60
				state.StaleAfterSeconds = 180
				state.Replacement = &pending
			} else {
				state.Enrollment = &pending
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpgradePending(&loaded); err != nil {
				t.Fatal(err)
			}
			reloaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			var saved *Enrollment
			if replacing {
				saved = reloaded.Replacement
				if reloaded.Credential != state.Credential || reloaded.HostID != state.HostID ||
					reloaded.AgentID != state.AgentID || reloaded.Generation != 1 {
					t.Fatal("identity changed")
				}
			} else {
				saved = reloaded.Enrollment
			}
			if saved == nil || saved.AgentVersion != "0.0.1" ||
				saved.RequestID != pending.RequestID || saved.Credential != pending.Credential {
				t.Fatal("legacy request was not frozen before replay")
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["agent_version"] != "0.0.1" || body["request_id"] != pending.RequestID ||
					body["credential"] != pending.Credential {
					t.Error("legacy wire request changed")
				}
				w.WriteHeader(503)
			}))
			defer server.Close()
			if _, err := testClient(server).Enroll(context.Background(), *saved); err == nil {
				t.Fatal("expected uncertain response")
			}
		})
	}
}

func TestPendingHeartbeatReplaysPreviousVersionThenUsesCurrentVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credential, _ := newCredential()
	host, _ := randomID()
	agent, _ := randomID()
	state := State{Version: 1, Credential: credential, HostID: host, AgentID: agent,
		Generation: 1, HeartbeatIntervalSeconds: 60, StaleAfterSeconds: 180,
		PendingHeartbeat: &PendingHeartbeat{Sequence: 1,
			SentAt: "2026-09-28T15:00:00.000Z", AgentVersion: "0.0.0"}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal("previous-version pending heartbeat was rejected", err)
	}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Sequence     uint64 `json:"sequence"`
			AgentVersion string `json:"agent_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if expected := "0.0.0"; calls == 3 {
			expected = Version
			if body.AgentVersion != expected {
				t.Error("next request kept old version")
			}
		} else if body.AgentVersion != expected {
			t.Error("pending heartbeat changed across retry")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintf(w, `{"schema_version":1,"sequence":%d,"accepted_at":"2026-09-28T15:00:00.000Z","duplicate":%t,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`,
			body.Sequence, calls == 2)
	}))
	defer server.Close()
	client := testClient(server)
	if err := sendHeartbeat(context.Background(), store, &state, client); err == nil {
		t.Fatal("expected lost response")
	}
	if err := sendHeartbeat(context.Background(), store, &state, client); err != nil {
		t.Fatal(err)
	}
	if state.LastSequence != 1 || state.PendingHeartbeat != nil {
		t.Fatal("old request was not acknowledged")
	}
	if err := sendHeartbeat(context.Background(), store, &state, client); err != nil {
		t.Fatal(err)
	}
	if state.LastSequence != 2 || calls != 3 {
		t.Fatal("current-version request did not follow ack")
	}
}

func TestInterruptedHeartbeatBodyReplaysExactSavedRequest(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credential, _ := newCredential()
	host, _ := randomID()
	agent, _ := randomID()
	initial := State{Version: 1, Credential: credential, HostID: host, AgentID: agent,
		Generation: 1, HeartbeatIntervalSeconds: 60, StaleAfterSeconds: 180,
		PendingHeartbeat: &PendingHeartbeat{Sequence: 1,
			SentAt: "2026-09-28T15:00:00.000Z", AgentVersion: "0.0.1"}}
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var original map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		calls++
		if calls == 1 {
			original = body
		} else if !reflect.DeepEqual(body, original) || r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("heartbeat replay changed the saved request")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Content-Length", "200")
			fmt.Fprint(w, `{"schema_version":1,`)
			return
		}
		fmt.Fprint(w, `{"schema_version":1,"sequence":1,"accepted_at":"2026-09-28T15:00:00.000Z","duplicate":true,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`)
	}))
	defer server.Close()
	client := testClient(server)
	if err := sendHeartbeat(context.Background(), store, &state, client); err == nil || terminal(err) {
		t.Fatal("interrupted heartbeat became terminal", err)
	}
	reloaded, err := store.Load()
	if err != nil || reloaded.PendingHeartbeat == nil || reloaded.LastSequence != 0 {
		t.Fatal("pending heartbeat was not preserved", err)
	}
	if err := sendHeartbeat(context.Background(), store, &reloaded, client); err != nil ||
		reloaded.PendingHeartbeat != nil || reloaded.LastSequence != 1 || calls != 2 {
		t.Fatal("interrupted heartbeat did not recover", err, calls)
	}
}

func TestSavedPendingVersionRejectsInvalidWireGrammar(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pending := testPending(t)
	pending.AgentVersion = "bad version"
	if err := store.Save(State{Version: 1, Enrollment: &pending}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("invalid saved version was accepted")
	}
}
