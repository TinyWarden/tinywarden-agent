package skillactions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

func logFixture(t *testing.T) (*Provider, string) {
	t.Helper()
	p := &Provider{Loader: Loader{Base: t.TempDir()}, Intent: maintenance.Intent{ID: "example"}}
	path := filepath.Join(p.Loader.Base, "updater", "private", "action-state-example", "operation-log.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	return p, path
}

type nativeLogProof struct {
	base     string
	lock     sync.Mutex
	records  map[int]json.RawMessage
	streamed atomic.Bool
}

func (p *nativeLogProof) receive(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.lock.Lock()
	defer p.lock.Unlock()
	var body struct {
		Schema  int               `json:"schema_version"`
		Job     string            `json:"job_id"`
		Scope   maintenance.Scope `json:"scope"`
		Binding json.RawMessage   `json:"binding"`
		Entries []json.RawMessage `json:"entries"`
	}
	if r.Header.Get("Authorization") != "Bearer native-fixture-only" || json.NewDecoder(io.LimitReader(r.Body, 160*1024)).Decode(&body) != nil || body.Schema != 1 || body.Scope.Generation != 1 || body.Job != "00000004-1111-4111-8111-111111111111" || !json.Valid(body.Binding) {
		t.Error("diagnostic HTTP authority or scope changed")
	}
	accepted := make([]int, 0, len(body.Entries))
	for _, raw := range body.Entries {
		var entry struct {
			Sequence int `json:"sequence"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			t.Error("invalid diagnostic JSON")
			continue
		}
		p.records[entry.Sequence] = append(json.RawMessage(nil), raw...)
		accepted = append(accepted, entry.Sequence)
	}
	path, err := maintenance.ArtifactPath(p.base, body.Job, "action_prepare")
	if err == nil {
		if info, e := os.Stat(path); e == nil && info.Size() == 0 {
			p.streamed.Store(true)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "accepted": accepted})
}

func (p *nativeLogProof) save(t *testing.T) {
	t.Helper()
	p.lock.Lock()
	defer p.lock.Unlock()
	if len(p.records) < 6 {
		t.Fatal("missing cross-phase diagnostics")
	}
	if path := os.Getenv("TW_ACTION_LOG_PROOF"); path != "" {
		rows := make([]json.RawMessage, 0, len(p.records))
		for n := 1; n <= len(p.records); n++ {
			rows = append(rows, p.records[n])
		}
		raw, _ := json.Marshal(rows)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLogDeliveryRetriesIdenticalRecordsWithoutChangingIntent(t *testing.T) {
	p, path := logFixture(t)
	data := []byte("{\"sequence\":1,\"phase\":\"prepare\",\"level\":\"info\",\"code\":\"phase_started\"}\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.Loader.Log = func(ctx context.Context, intent maintenance.Intent, entries []json.RawMessage) error {
		calls++
		if ctx.Err() != nil || intent.ID != p.Intent.ID || len(entries) != 1 || string(entries[0])+"\n" != string(data) {
			t.Fatal("changed diagnostic scope or bytes")
		}
		return errors.New("unavailable")
	}
	p.watchLogs()()
	p.watchLogs()()
	if calls != 2 || p.Intent.ID != "example" {
		t.Fatal("log failure changed execution state")
	}
}

func TestPartialInvalidAndLinkedDiagnosticFilesCannotBeForwarded(t *testing.T) {
	p, path := logFixture(t)
	for _, raw := range []string{"{\"sequence\":1}", "{\"sequence\":2}\n", "not json\n"} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if len(p.logEntries()) != 0 {
			t.Fatal("invalid entry forwarded")
		}
	}
	os.Remove(path)
	if err := os.Symlink("/etc/passwd", path); err != nil {
		t.Fatal(err)
	}
	if len(p.logEntries()) != 0 {
		t.Fatal("followed link")
	}
}
