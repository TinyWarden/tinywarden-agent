package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkWaitLanesAndIndependentTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/v2/agent/work-wait" || body["lane"] != "monitoring" || body["wait_seconds"] != float64(25) {
			t.Error("incorrect monitoring hint request")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "available": true, "cursor": strings.Repeat("a", 64)})
	}))
	defer server.Close()
	client := NewClient(server.URL)
	wait := client.WorkWaitClient()
	defer wait.HTTP.CloseIdleConnections()
	if client.HTTP.Timeout != 10*time.Second || wait.HTTP.Timeout != 35*time.Second || client.HTTP.Transport == wait.HTTP.Transport {
		t.Fatal("wait changed or reused normal transport")
	}
	hint, err := wait.WaitForWork(context.Background(), "test-credential", nil, "monitoring")
	if err != nil || !hint.Available || len(hint.Cursor) != 64 {
		t.Fatal("hint not validated", err)
	}
	if _, err := wait.WaitForWork(context.Background(), "test-credential", nil, "unknown"); err == nil {
		t.Fatal("unknown lane accepted")
	}
}

func TestMonitoringRetryRecoversWithinTenSecondsAndHonorsServer(t *testing.T) {
	for _, n := range []int{0, 1, 2, 4, 5, 100} {
		for range 25 {
			delay := WorkerRetry(&ResponseError{Status: 503}, n)
			if delay < time.Second || delay > 10*time.Second {
				t.Fatalf("monitoring retry too slow or tight: %v", delay)
			}
			if got := WorkerRetry(&ResponseError{Status: 429, RetryAfter: 90 * time.Second}, n); got != 90*time.Second {
				t.Fatal("server cooldown ignored", got)
			}
		}
	}
}
