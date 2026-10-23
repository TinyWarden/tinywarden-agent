package legacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func testAssignment(t *testing.T, origin string, state agentstate.State) AssignmentCache {
	t.Helper()
	a := Assignment{DefinitionKey: "disk-local", DefinitionRevision: 1, PolicyVersion: 0,
		Mode: "inherit", Applicability: "missing_capability",
		Effective: EffectiveCheck{Capability: "disk_usage.v1", SelectorVersion: 1,
			EvaluatorVersion: 1, WarningPercent: 85, CriticalPercent: 95,
			IntervalSeconds: 300, TimeoutSeconds: 10, StaleAfterSeconds: 900},
		Checks: []AssignedCheck{}}
	id, _ := agentstate.RandomID()
	cache := AssignmentCache{Version: 1, Origin: origin, HostID: state.HostID,
		AgentID: state.AgentID, Generation: state.Generation, ID: id, Revision: 1,
		Assignment: a}
	payload, err := json.Marshal([]any{1, cache.HostID, cache.AgentID, cache.Generation,
		"disk-local", cache.Revision, 1, 0, "inherit", "missing_capability",
		"disk_usage.v1", 1, 1, 85, 95, 300, 10, 900})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	cache.Digest = hex.EncodeToString(sum[:])
	return cache
}

func testAssignmentState(t *testing.T) agentstate.State {
	t.Helper()
	credential, _ := agentstate.NewCredential()
	host, _ := agentstate.RandomID()
	agent, _ := agentstate.RandomID()
	return agentstate.State{Version: 1, Credential: credential, HostID: host, AgentID: agent,
		Generation: 1, HeartbeatIntervalSeconds: 60, StaleAfterSeconds: 180}
}

func TestAssignmentCacheIsAtomicScopedAndSeparateFromIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := testAssignmentState(t)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cache := testAssignment(t, "https://warden.example.org", state)
	if !validAssignment(cache) {
		t.Fatal("test assignment invalid")
	}
	if err := SaveAssignment(store, cache); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "assignments.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("cache mode", err)
	}
	loaded, err := LoadAssignment(store, cache.Origin, state)
	if err != nil || !reflect.DeepEqual(*loaded, cache) {
		t.Fatal("cache failed round trip", err)
	}
	if changed, err := os.ReadFile(filepath.Join(dir, "state.json")); err != nil ||
		string(changed) != string(before) {
		t.Fatal("identity schema changed", err)
	}
	other := state
	other.Generation++
	if stale, err := LoadAssignment(store, cache.Origin, other); err != nil || stale != nil {
		t.Fatal("generation change retained cache", err)
	}
	if stale, err := LoadAssignment(store, "https://other.example.org", state); err != nil || stale != nil {
		t.Fatal("origin change retained cache", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assignments.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if stale, err := LoadAssignment(store, cache.Origin, state); err == nil || stale != nil {
		t.Fatal("corrupt cache accepted")
	}
}

func TestAssignmentResponseRequiresExactCacheAndDigest(t *testing.T) {
	state := testAssignmentState(t)
	var requested []map[string]any
	var reply assignmentResponse
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		requested = append(requested, input)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(reply); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := testClient(server)
	cache := testAssignment(t, client.Origin, state)
	reply = assignmentResponse{SchemaVersion: 1, HostID: state.HostID, AgentID: state.AgentID,
		Generation: 1, ID: cache.ID, Revision: cache.Revision, Digest: cache.Digest,
		NotModified: false, PollIntervalSeconds: 60, Assignment: &cache.Assignment}
	first, err := client.FetchAssignments(context.Background(), state, nil)
	if err != nil || first.Assignment == nil || requested[0]["known_assignment"] != nil {
		t.Fatal("full assignment without cache failed", err)
	}
	if !reflect.DeepEqual(requested[0]["capabilities"], []any{"disk_usage.v1", "skill-control.v1"}) {
		t.Fatal("collector capability missing")
	}
	reply.NotModified, reply.Assignment = true, nil
	if _, err := client.FetchAssignments(context.Background(), state, nil); err == nil {
		t.Fatal("omitted assignment accepted without cache")
	}
	if _, err := client.FetchAssignments(context.Background(), state, &cache); err != nil {
		t.Fatal("exact omission rejected", err)
	}
	if requested[len(requested)-1]["known_assignment"] == nil {
		t.Fatal("known assignment omitted")
	}
	reply.NotModified, reply.Assignment = false, &cache.Assignment
	reply.Digest = fmt.Sprintf("%064x", 0)
	if _, err := client.FetchAssignments(context.Background(), state, &cache); err == nil {
		t.Fatal("changed equal revision accepted")
	}
	reply.Revision = 0
	if _, err := client.FetchAssignments(context.Background(), state, &cache); err == nil {
		t.Fatal("regressed revision accepted")
	}
}

func TestReorderedAssignmentResponseCannotRenewOrReplaceNewerCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := testAssignmentState(t)
	old := testAssignment(t, "https://warden.example.org", state)
	newer := old
	newer.Revision = 2
	newer.ID, _ = agentstate.RandomID()
	payload, err := json.Marshal([]any{1, newer.HostID, newer.AgentID, newer.Generation,
		"disk-local", newer.Revision, 1, 0, "inherit", "missing_capability",
		"disk_usage.v1", 1, 1, 85, 95, 300, 10, 900})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	newer.Digest = hex.EncodeToString(sum[:])
	if err := SaveAssignment(store, newer); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "assignments.json"))
	if err != nil {
		t.Fatal(err)
	}
	known := &newer
	omitted := &assignmentResponse{NotModified: true, ID: old.ID,
		Revision: old.Revision, Digest: old.Digest}
	if err := receiveAssignment(store, old.Origin, state, &known, omitted); err == nil {
		t.Fatal("late omitted response renewed newer cache")
	}
	full := &assignmentResponse{ID: old.ID, Revision: old.Revision,
		Digest: old.Digest, Assignment: &old.Assignment}
	if err := receiveAssignment(store, old.Origin, state, &known, full); err == nil {
		t.Fatal("late full response replaced newer cache")
	}
	after, err := os.ReadFile(filepath.Join(dir, "assignments.json"))
	if err != nil || string(after) != string(before) || !reflect.DeepEqual(*known, newer) {
		t.Fatal("reordered response changed persistent authority", err)
	}
}
