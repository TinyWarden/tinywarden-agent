package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPending(t *testing.T) Enrollment {
	t.Helper()
	requestID, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	tokenID, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	credential, err := newCredential()
	if err != nil {
		t.Fatal(err)
	}
	return Enrollment{RequestID: requestID, Token: "tw_e_" + tokenID + "." + strings.Repeat("A", 43),
		Credential: credential, AgentVersion: Version, Hostname: "fixture.example.org", OSID: "debian",
		OSVersion: "13", Architecture: "amd64"}
}

func testClient(server *httptest.Server) *Client {
	httpClient := server.Client()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	httpClient.Timeout = 10 * time.Second
	return &Client{Origin: server.URL, HTTP: httpClient}
}

func TestLostEnrollmentAndHeartbeatResponseRetainOnePendingRequest(t *testing.T) {
	pending := testPending(t)
	credentialID := strings.SplitN(strings.TrimPrefix(pending.Credential, "tw_a_"), ".", 2)[0]
	hostID, _ := randomID()
	agentID, _ := randomID()
	enrollCalls := 0
	beatCalls := 0
	seenBeat := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/api/v1/agent/enroll" {
			enrollCalls++
			if r.Header.Get("Authorization") != "Bearer "+pending.Token {
				t.Error("wrong token")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["request_id"] != pending.RequestID || body["credential"] != pending.Credential {
				t.Error("changed enrollment request")
			}
			if enrollCalls == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"schema_version":1,"error":{"code":"temporarily_unavailable"},"request_id":"00000000-0000-4000-8000-000000000000"}`)
				return
			}
			fmt.Fprintf(w, `{"schema_version":1,"host_id":%q,"agent_id":%q,"credential_id":%q,"generation":1,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`,
				hostID, agentID, credentialID)
			return
		}
		if r.URL.Path == "/api/v1/agent/heartbeat" {
			beatCalls++
			if r.Header.Get("Authorization") != "Bearer "+pending.Credential {
				t.Error("wrong credential")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			wire, _ := json.Marshal(body)
			if beatCalls == 1 {
				seenBeat = string(wire)
			} else if string(wire) != seenBeat {
				t.Error("changed pending heartbeat")
			}
			if beatCalls == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"schema_version":1,"error":{"code":"temporarily_unavailable"},"request_id":"00000000-0000-4000-8000-000000000000"}`)
				return
			}
			fmt.Fprint(w, `{"schema_version":1,"sequence":1,"accepted_at":"2026-09-28T15:00:00.000Z","duplicate":true,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(State{Version: 1, Enrollment: &pending}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	client := testClient(server)
	if err := finishEnrollment(context.Background(), store, &state, client); err == nil {
		t.Fatal("expected lost response")
	}
	stillPending, err := store.Load()
	if err != nil || stillPending.Enrollment == nil {
		t.Fatal("enrollment was lost", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := finishEnrollment(context.Background(), store, &state, client); err != nil {
		t.Fatal(err)
	}
	if state.Enrollment != nil || state.HostID != hostID || state.Credential != pending.Credential {
		t.Fatal("enrollment binding wrong")
	}
	if err := sendHeartbeat(context.Background(), store, &state, client); err == nil {
		t.Fatal("expected lost heartbeat response")
	}
	if state.PendingHeartbeat == nil || state.PendingHeartbeat.Sequence != 1 {
		t.Fatal("pending sequence lost")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := sendHeartbeat(context.Background(), store, &state, client); err != nil {
		t.Fatal(err)
	}
	if state.LastSequence != 1 || state.PendingHeartbeat != nil || enrollCalls != 2 || beatCalls != 2 {
		t.Fatal("retry changed or duplicated state")
	}
}

func TestClientRejectsRedirectAndOversizedResponse(t *testing.T) {
	forwarded := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded++ }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(307)
	}))
	defer server.Close()
	client := testClient(server)
	_, err := client.Enroll(context.Background(), testPending(t))
	if err == nil || forwarded != 0 || !terminal(err) {
		t.Fatal("redirect was followed or retried")
	}

	large := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.Repeat("x", maxBody+1))
	}))
	defer large.Close()
	client = testClient(large)
	_, err = client.Enroll(context.Background(), testPending(t))
	if err == nil || !terminal(err) {
		t.Fatal("oversized response accepted or retried")
	}
}

func TestProductionClientRequiresTrustedTLSAndPrivateState(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
	}))
	defer server.Close()
	client := NewClient(server.URL)
	_, err := client.Enroll(context.Background(), testPending(t))
	if err == nil || !terminal(err) {
		t.Fatal("untrusted TLS certificate accepted")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("second process acquired state lock")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Load(); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestEnrollmentTokenReadRejectsSymlinkAndLooseMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	token := testPending(t).Token
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if found, err := readToken(path); err != nil || found != token {
		t.Fatal("private token file was not read")
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(link); err == nil {
		t.Fatal("symlink token file was followed")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(path); err == nil {
		t.Fatal("loosely readable token file was accepted")
	}
}
