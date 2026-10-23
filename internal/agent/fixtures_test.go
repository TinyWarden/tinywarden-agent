package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func baselineStore(t *testing.T) *agentstate.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := agentstate.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testAssignmentState(t *testing.T) agentstate.State {
	t.Helper()
	credential, _ := agentstate.NewCredential()
	host, _ := agentstate.RandomID()
	agent, _ := agentstate.RandomID()
	return agentstate.State{Version: 1, Credential: credential, HostID: host, AgentID: agent,
		Generation: 1, HeartbeatIntervalSeconds: 60, StaleAfterSeconds: 180}
}

func rejectBaselineServer() *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
}
