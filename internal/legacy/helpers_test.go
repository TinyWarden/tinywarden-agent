package legacy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	http "net/http"
	"net/http/httptest"
	"testing"
	"time"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func testClient(server *httptest.Server) *Client {
	httpClient := server.Client()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	httpClient.Timeout = 10 * time.Second
	return &Client{Client: controlplane.Client{Origin: server.URL, HTTP: httpClient}}
}

func readyRecoveryCache(t *testing.T, origin string, state agentstate.State) AssignmentCache {
	t.Helper()
	cache := testAssignment(t, origin, state)
	cache.Assignment.Applicability = "ready"
	cache.Assignment.Checks = []AssignedCheck{{Kind: "disk_usage"}}
	data, err := json.Marshal([]any{1, cache.HostID, cache.AgentID, cache.Generation,
		"disk-local", cache.Revision, 1, 0, "inherit", "ready", "disk_usage.v1",
		1, 1, 85, 95, 300, 10, 900})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	cache.Digest = hex.EncodeToString(digest[:])
	cache.ValidatedAt = time.Now().UTC().Truncate(time.Millisecond).
		Format("2006-01-02T15:04:05.000Z")
	if !validAssignment(cache) {
		t.Fatal("invalid ready cache")
	}
	return cache
}
