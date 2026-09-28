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
	"sync"
	"testing"
	"time"
)

func TestReplacementSurvivesLostResponseAndRunRestart(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old, _ := newCredential()
	hostID, _ := randomID()
	agentID, _ := randomID()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(State{Version: 1, Credential: old, HostID: hostID,
		AgentID: agentID, Generation: 1, HeartbeatIntervalSeconds: 60,
		StaleAfterSeconds: 180}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	token := testPending(t).Token
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	var savedRequest, savedCredential string
	heartbeatSeen := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/agent/enroll" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			calls++
			count := calls
			if count == 1 {
				savedRequest, _ = body["request_id"].(string)
				savedCredential, _ = body["credential"].(string)
			}
			match := body["request_id"] == savedRequest && body["credential"] == savedCredential
			mu.Unlock()
			if !match || r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("replacement retry changed saved authority")
			}
			if count == 1 {
				w.Header().Set("Content-Length", "200")
				w.WriteHeader(200)
				fmt.Fprint(w, `{"schema_version":1,`)
				return
			}
			credentialID := strings.SplitN(strings.TrimPrefix(savedCredential, "tw_a_"), ".", 2)[0]
			fmt.Fprintf(w, `{"schema_version":1,"host_id":%q,"agent_id":%q,"credential_id":%q,"generation":2,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`,
				hostID, agentID, credentialID)
			return
		}
		if r.URL.Path == "/api/v1/agent/heartbeat" {
			if r.Header.Get("Authorization") != "Bearer "+savedCredential {
				t.Error("heartbeat used old credential")
			}
			fmt.Fprint(w, `{"schema_version":1,"sequence":1,"accepted_at":"2026-09-28T15:00:00.000Z","duplicate":false,"heartbeat_interval_seconds":60,"stale_after_seconds":180}`)
			heartbeatSeen <- struct{}{}
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	client := testClient(server)
	config := Config{ControlPlaneOrigin: server.URL, StateDir: dir}
	if err := StartReplacement(context.Background(), config, tokenPath, client); err == nil {
		t.Fatal("expected uncertain first replacement response")
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.Load()
	store.Close()
	if err != nil || pending.Replacement == nil || pending.Credential != old ||
		pending.Generation != 1 || pending.Replacement.Credential == old {
		t.Fatal("replacement pending state was not durable", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-heartbeatSeen:
			time.Sleep(50 * time.Millisecond)
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := Run(ctx, config, client, func(string) {}); !errors.Is(err, context.Canceled) {
		t.Fatal("run did not finish after restart and cancellation", err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	final, err := store.Load()
	if err != nil || final.Replacement != nil || final.Credential != savedCredential ||
		final.AgentID != agentID || final.HostID != hostID || final.Generation != 2 ||
		final.LastSequence != 1 || final.PendingHeartbeat != nil {
		t.Fatal("replacement recovery changed identity or sequence", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 || savedRequest != pending.Replacement.RequestID ||
		savedCredential != pending.Replacement.Credential {
		t.Fatal("replacement was duplicated or regenerated")
	}
}
