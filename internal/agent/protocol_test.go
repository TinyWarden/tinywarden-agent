package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestResponseFailuresStayBoundedAndClassified(t *testing.T) {
	cases := []struct {
		name, contentType, body, retryAfter string
		status, expectedDelay               int
		terminal                            bool
	}{
		{"rate limited", "application/json", "", "120", 429, 120, false},
		{"server unavailable", "application/json", "", "bad", 503, 0, false},
		{"capped retry delay", "application/json", "", "1200", 429, 900, false},
		{"huge retry delay", "application/json", "", "999999999999999999999999", 503, 900, false},
		{"bad json", "application/json", "{", "", 200, 0, true},
		{"bad media", "text/html", "{}", "", 200, 0, true},
		{"missing version", "application/json", "{}", "", 200, 0, true},
		{"oversize chunked", "application/json", strings.Repeat("x", maxBody+1), "", 200, 0, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			_, err := testClient(server).Enroll(context.Background(), testPending(t))
			if err == nil || terminal(err) != test.terminal {
				t.Fatal("response classification was incorrect", err)
			}
			if test.status == 429 || test.status == 503 {
				var response *ResponseError
				if !errors.As(err, &response) || response.RetryAfter != time.Duration(test.expectedDelay)*time.Second {
					t.Fatal("retry delay was incorrect", err)
				}
				if test.expectedDelay > 0 && retryWait(err, 2*time.Second) != response.RetryAfter {
					t.Fatal("server retry delay was ignored")
				}
			}
		})
	}
}

func TestInterruptedResponseBodyRetriesSavedEnrollment(t *testing.T) {
	pending := testPending(t)
	host, _ := randomID()
	agent, _ := randomID()
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["request_id"] != pending.RequestID || request["credential"] != pending.Credential ||
			request["agent_version"] != pending.AgentVersion {
			t.Error("retry changed saved enrollment input")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Content-Length", "200")
			fmt.Fprint(w, `{"schema_version":1,`)
			return
		}
		credentialID := strings.SplitN(strings.TrimPrefix(pending.Credential, "tw_a_"), ".", 2)[0]
		fmt.Fprintf(w, `{"schema_version":1,"host_id":%q,"agent_id":%q,"credential_id":%q,"generation":1,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`,
			host, agent, credentialID)
	}))
	defer server.Close()
	client := testClient(server)
	if _, err := client.Enroll(context.Background(), pending); err == nil || terminal(err) {
		t.Fatal("interrupted body was accepted or treated as terminal", err)
	}
	if result, err := client.Enroll(context.Background(), pending); err != nil ||
		result.HostID != host || calls != 2 {
		t.Fatal("saved enrollment did not recover", err, calls)
	}
}

func TestBodyTimeoutAfterHeadersIsRetryable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "200")
		fmt.Fprint(w, `{"schema_version":1,`)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer server.Close()
	client := testClient(server)
	client.HTTP.Timeout = 50 * time.Millisecond
	if _, err := client.Enroll(context.Background(), testPending(t)); err == nil || terminal(err) {
		t.Fatal("body timeout was accepted or treated as terminal", err)
	}
}

func TestLastRetryAfterCarriesIntoDegradedMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		var fifth time.Time
		err := runCycle(context.Background(), func(context.Context) error {
			calls++
			if calls < 5 {
				return &ResponseError{Status: 503}
			}
			if calls == 5 {
				fifth = time.Now()
				return &ResponseError{Status: 429, RetryAfter: 900 * time.Second}
			}
			if since := time.Since(fifth); since < 900*time.Second {
				t.Errorf("degraded retry preceded server delay: %v", since)
			}
			return nil
		}, func(string) {})
		if err != nil || calls != 6 {
			t.Fatal("degraded retry did not recover", err, calls)
		}
	})
}

func TestCancellationStopsRetryWithoutAnotherWrite(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer server.Close()
	client := testClient(server)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := runCycle(ctx, func(ctx context.Context) error {
		_, err := client.Enroll(ctx, testPending(t))
		return err
	}, func(string) {})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatal("cancelled retry issued another request", err, calls)
	}
}

func TestRequestTimeoutRemainsRetryable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer server.Close()
	client := testClient(server)
	client.HTTP.Timeout = 50 * time.Millisecond
	_, err := client.Enroll(context.Background(), testPending(t))
	if err == nil || terminal(err) {
		t.Fatal("hung request was accepted or classified terminal", err)
	}
}

func TestConfiguredOriginAndStatePathFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	valid := Config{ControlPlaneOrigin: "https://neutralisp.tinywarden.com",
		StateDir: filepath.Join(t.TempDir(), "state")}
	for _, candidate := range []Config{
		{ControlPlaneOrigin: "http://neutralisp.tinywarden.com", StateDir: valid.StateDir},
		{ControlPlaneOrigin: "https://neutralisp.tinywarden.com/other", StateDir: valid.StateDir},
		{ControlPlaneOrigin: "https://user@neutralisp.tinywarden.com", StateDir: valid.StateDir},
		{ControlPlaneOrigin: valid.ControlPlaneOrigin, StateDir: "relative-state"},
	} {
		data, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("unsafe configuration accepted: %q", candidate.ControlPlaneOrigin)
		}
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if loaded, err := LoadConfig(path); err != nil || loaded != valid {
		t.Fatal("valid native configuration rejected", err)
	}
}
