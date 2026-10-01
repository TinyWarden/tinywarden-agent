package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func baselineFixture(t *testing.T, origin string) (State, baselineCache) {
	t.Helper()
	data, err := os.ReadFile("testdata/baseline-v1/response.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version  int              `json:"version"`
		Response baselineResponse `json:"response"`
	}
	if decodeBaseline(data, &f) != nil {
		t.Fatal("invalid shared fixture")
	}
	state := State{Version: 1, HostID: f.Response.HostID, AgentID: f.Response.AgentID, Generation: f.Response.Generation, Credential: "synthetic"}
	cache, err := resolveBaselineResponse(origin, state, nil, f.Response)
	if err != nil {
		t.Fatal("authored shared digest mismatch", err)
	}
	cache.ValidatedAt = baselineStamp(time.Now())
	return state, cache
}
func baselineReply(state State, entries []baselineEntry) baselineResponse {
	return baselineResponse{1, state.HostID, state.AgentID, state.Generation, 60, entries}
}
func cloneEntries(t *testing.T, entries []baselineEntry) []baselineEntry {
	t.Helper()
	data, _ := json.Marshal(entries)
	var copy []baselineEntry
	if decodeBaseline(data, &copy) != nil {
		t.Fatal("copy")
	}
	return copy
}

func TestBaselineSharedDigestsAndExactRecipes(t *testing.T) {
	state, cache := baselineFixture(t, "https://example.org")
	if !validBaselineCache(cache) {
		t.Fatal("shared complete cache rejected")
	}
	for i := range cache.Entries {
		changed := cloneEntries(t, cache.Entries)
		changed[i].Assignment.Recipe.Steps[0].Argv = append(changed[i].Assignment.Recipe.Steps[0].Argv, "--invalid")
		changed[i].Digest = baselineDigest(cache.Scope, changed[i])
		if _, err := resolveBaselineResponse(cache.Scope.Origin, state, nil, baselineReply(state, changed)); err == nil {
			t.Fatal("valid digest bypassed compiled profiles")
		}
	}
	changed := state
	changed.Generation++
	if validBaselineEntry(scopeBaseline(cache.Scope.Origin, changed), cache.Entries[0]) {
		t.Fatal("digest omitted credential generation")
	}
}

func TestBaselineStrictEncodingShapeAndOmission(t *testing.T) {
	state, cache := baselineFixture(t, "https://example.org")
	data, _ := json.Marshal(baselineReply(state, cache.Entries))
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(data), `"schema_version":1`, `"schema_version":1,"schema_\u0076ersion":1`, 1)),
		[]byte(strings.Replace(string(data), `"schema_version"`, `"Schema_Version"`, 1)),
		[]byte(strings.Replace(string(data), `"not_modified":false,`, "", 1)),
		[]byte(strings.Replace(string(data), `"not_modified":false`, `"not_modified":null`, 1)),
		[]byte(strings.Replace(string(data), `"assignment":{`, `"assignment":{"shell":"synthetic",`, 1)),
		append(slices.Clone(data), 0xff),
		[]byte(strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)),
	} {
		var r baselineResponse
		if decodeBaseline(bad, &r) == nil {
			t.Fatal("inexact baseline JSON accepted")
		}
	}
	omitted := cloneEntries(t, cache.Entries)
	for i := range omitted {
		omitted[i].NotModified = true
		omitted[i].Assignment = nil
	}
	if _, err := resolveBaselineResponse(cache.Scope.Origin, state, &cache, baselineReply(state, omitted)); err != nil {
		t.Fatal("exact omission", err)
	}
	if _, err := resolveBaselineResponse(cache.Scope.Origin, state, nil, baselineReply(state, omitted)); err == nil {
		t.Fatal("omission without cache")
	}
	diverged := cloneEntries(t, cache.Entries)
	diverged[0].ID, _ = randomID()
	diverged[0].Digest = baselineDigest(cache.Scope, diverged[0])
	if _, err := resolveBaselineResponse(cache.Scope.Origin, state, &cache, baselineReply(state, diverged)); err == nil {
		t.Fatal("equal revision identity changed")
	}
	diverged[0].Revision--
	diverged[0].Digest = baselineDigest(cache.Scope, diverged[0])
	if _, err := resolveBaselineResponse(cache.Scope.Origin, state, &cache, baselineReply(state, diverged)); err == nil {
		t.Fatal("regression accepted")
	}
}

func TestBaselineTransportRejectsDuplicatesBeforeMapAndKeepsP2Limits(t *testing.T) {
	var response string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
	defer server.Close()
	client := testClient(server)
	state, cache := baselineFixture(t, server.URL)
	data, _ := json.Marshal(baselineReply(state, cache.Entries))
	response = string(data)
	if _, err := client.fetchBaseline(context.Background(), state, nil, []string{"exec_observe.debian13.v1"}); err != nil {
		t.Fatal(err)
	}
	response = strings.Replace(response, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)
	if _, err := client.fetchBaseline(context.Background(), state, nil, []string{}); err == nil {
		t.Fatal("transport erased duplicate key")
	}
	response = `{"schema_version":1,"padding":"` + strings.Repeat("x", maxBody) + `"}`
	if _, err := client.post(context.Background(), "/synthetic", "synthetic", struct{}{}, false); err == nil {
		t.Fatal("legacy response bound changed")
	}
	response = `{"schema_version":1,"padding":"` + strings.Repeat("x", baselineBytes) + `"}`
	if _, err := client.post(context.Background(), "/synthetic", "synthetic", struct{}{}, false, maxBody, baselineBytes); err == nil {
		t.Fatal("baseline response unbounded")
	}
}
