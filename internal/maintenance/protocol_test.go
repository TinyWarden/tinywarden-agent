package maintenance

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func wireFixture(t *testing.T) (Request, Reply) {
	t.Helper()
	raw, e := os.ReadFile("testdata/maintenance-wire-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture map[string]json.RawMessage
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	var request Request
	var reply Reply
	if controlplane.Decode(fixture["ready"], &request) != nil || controlplane.Decode(fixture["grant_reply"], &reply) != nil {
		t.Fatal("app/agent contract disagreement")
	}
	return request, reply
}
func fixtureRegistry() Registry {
	return Registry{{ID: "packages.upgrade_conservative.v1", Version: 1, Approval: true, New: func() Provider { return fakeProvider{} }}}
}
func TestReplyBindsExactScopeAndNeverGrantsCancelledWork(t *testing.T) {
	request, reply := wireFixture(t)
	if e := reply.Validate(request, fixtureRegistry()); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Reply){func(r *Reply) { r.Generation++ }, func(r *Reply) { r.AckSequence++ },
		func(r *Reply) { r.Job.CancelRequested = true }, func(r *Reply) { r.Job.Scope.Generation++ },
		func(r *Reply) { r.Job.Grant.ExpiresAt = "2026-10-24T00:01:01.000Z" }} {
		_, value := wireFixture(t)
		change(&value)
		if value.Validate(request, fixtureRegistry()) == nil {
			t.Fatal("invalid grant admitted")
		}
	}
}
func TestReplayedGrantCannotRenewMonotonicStartAllowance(t *testing.T) {
	_, reply := wireFixture(t)
	boot, now, e := BootClock()
	if e != nil {
		t.Fatal(e)
	}
	reply.ServerAt = "2026-10-24T00:00:20.000Z"
	start, e := StartFromReply(reply, boot, now)
	if e != nil || start.NotAfterMS != now+40000 {
		t.Fatal("grant age was not charged", e)
	}
	if _, e = StartFromReply(reply, boot, now-45000); e == nil {
		t.Fatal("round trip renewed elapsed allowance")
	}
	reply.ServerAt = "2026-10-24T00:01:01.000Z"
	if _, e = StartFromReply(reply, boot, now); e == nil {
		t.Fatal("replayed expired receipt renewed allowance")
	}
}
func TestMaintenanceUsesBoundedExistingTransport(t *testing.T) {
	request, reply := wireFixture(t)
	request.Kind = "plan_chunk"
	request.Sequence = 3
	reply.AckSequence = 3
	content := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", ChunkBytes)))
	request.Data, _ = json.Marshal(map[string]any{"provider_sha256": strings.Repeat("a", 64), "part": 0, "total": 2, "content": content})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(io.LimitReader(r.Body, MaxControl+1))
		if e != nil || len(raw) <= controlplane.MaxBody || len(raw) > MaxControl {
			t.Error("wrong lane bounds")
		}
		if r.URL.Path != "/api/v1/agent/maintenance-jobs" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("wrong lane or authority")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()
	client := controlplane.NewClient(server.URL)
	client.HTTP.Timeout = time.Second
	if _, e := Post(t.Context(), client, "fixture", request, fixtureRegistry()); e != nil {
		t.Fatal(e)
	}
	request.Data = json.RawMessage(`{"content":"` + strings.Repeat("a", MaxControl) + `"}`)
	if _, e := Post(t.Context(), client, "fixture", request, fixtureRegistry()); e == nil {
		t.Fatal("oversized control message admitted")
	}
}
