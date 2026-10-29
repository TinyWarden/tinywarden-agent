package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

func TestActionLogReceiptRequiresExactAcknowledgement(t *testing.T) {
	check := actionLogReceipt([]json.RawMessage{json.RawMessage(`{"sequence":2}`), json.RawMessage(`{"sequence":3}`)})
	if err := check([]byte(`{"schema_version":1,"accepted":[2,3]}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"schema_version":2,"accepted":[2,3]}`,
		`{"schema_version":1,"accepted":[3,2]}`,
		`{"schema_version":1,"accepted":[2]}`,
		`{"schema_version":1,"accepted":[2,3],"extra":true}`,
		`{"schema_version":1,"accepted":[2,3]} {}`,
	} {
		if check([]byte(raw)) == nil {
			t.Fatalf("accepted invalid receipt: %s", raw)
		}
	}
}

func logUploadIntent() maintenance.Intent {
	return maintenance.Intent{ID: "00000004-1111-4111-8111-111111111111", Scope: maintenance.Scope{
		HostID: "00000002-1111-4111-8111-111111111111", AgentID: "00000003-1111-4111-8111-111111111111", Generation: 1},
		Binding: &actioncontract.Binding{InstallationID: "00000001-1111-4111-8111-111111111111", SkillID: "example/observations",
			ActionID: "example.check", ActionVersion: 1, ContentSHA: strings.Repeat("a", 64), DescriptorSHA: strings.Repeat("b", 64),
			PermissionRevision: "1", SettingsRevision: "1", SettingsSHA: strings.Repeat("c", 64), InputSHA: strings.Repeat("d", 64),
			Origin: actioncontract.Origin{GitHubID: "12345", OwnerID: "67890"}}}
}

func receiveLogUpload(t *testing.T, r *http.Request, intent maintenance.Intent) ([]json.RawMessage, int) {
	t.Helper()
	if r.Method != "POST" || r.URL.Path != "/api/v2/agent/action-logs" || r.Header.Get("Authorization") != "Bearer fixture-only" {
		t.Error("changed log upload path or authority")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 160*1024+1))
	if err != nil || len(raw) > 160*1024 {
		t.Error("log request exceeds the wire limit")
	}
	var input struct {
		Schema  int                     `json:"schema_version"`
		Job     string                  `json:"job_id"`
		Scope   maintenance.Scope       `json:"scope"`
		Binding *actioncontract.Binding `json:"binding"`
		Entries []json.RawMessage       `json:"entries"`
	}
	if controlplane.Decode(raw, &input) != nil || input.Schema != 1 || input.Job != intent.ID || input.Scope != intent.Scope || !actioncontract.Equal(input.Binding, intent.Binding) {
		t.Error("changed log upload scope or envelope")
	}
	return input.Entries, len(raw)
}

func TestActionLogUploaderValidatesActualHTTPAcknowledgement(t *testing.T) {
	for _, test := range []struct {
		name, response string
		valid          bool
	}{
		{"exact", `{"schema_version":1,"accepted":[1,2]}`, true},
		{"missing", `{"schema_version":1}`, false},
		{"empty", `{"schema_version":1,"accepted":[]}`, false},
		{"partial", `{"schema_version":1,"accepted":[1]}`, false},
		{"changed", `{"schema_version":1,"accepted":[1,3]}`, false},
		{"reordered", `{"schema_version":1,"accepted":[2,1]}`, false},
		{"version", `{"schema_version":2,"accepted":[1,2]}`, false},
		{"unknown", `{"schema_version":1,"accepted":[1,2],"extra":true}`, false},
		{"trailing", `{"schema_version":1,"accepted":[1,2]} {}`, false},
		{"oversized", strings.Repeat(" ", 16*1024) + `{"schema_version":1,"accepted":[1,2]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			intent := logUploadIntent()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receiveLogUpload(t, r, intent)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(test.response))
			}))
			defer server.Close()
			err := actionLogUploader(controlplane.NewClient(server.URL), "fixture-only")(context.Background(), intent,
				[]json.RawMessage{json.RawMessage(`{"sequence":1}`), json.RawMessage(`{"sequence":2}`)})
			if (err == nil) != test.valid {
				t.Fatalf("HTTP acknowledgement validity = %t; error = %v", test.valid, err)
			}
		})
	}
}

func escapedLogBacklog(t *testing.T) []json.RawMessage {
	t.Helper()
	var entries []json.RawMessage
	for sequence := 1; sequence < 128; sequence++ {
		// Match the SDK's unescaped HTML characters and exact 1023-byte line cap.
		prefix := fmt.Sprintf(`{"sequence":%d,"phase":"prepare","level":"info","message":{"key":"step","params":{"first":"%s","second":"`, sequence, strings.Repeat("&", 512))
		suffix := `"}}}`
		padding := 1023 - len(prefix) - len(suffix)
		if padding < 0 || padding > 512 {
			t.Fatal("fixture exceeds catalogue string bounds")
		}
		line := json.RawMessage(prefix + strings.Repeat("<", padding) + suffix)
		if !json.Valid(line) {
			t.Fatal("invalid fixture JSON")
		}
		entries = append(entries, line)
	}
	return append(entries, json.RawMessage(`{"sequence":128,"phase":"prepare","level":"warning","code":"log_truncated"}`))
}

func TestActionLogUploaderDrainsBoundedBacklogAndRetriesPartialDelivery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial_failure_%t", fail), func(t *testing.T) {
			intent, entries := logUploadIntent(), escapedLogBacklog(t)
			before, _ := json.Marshal(intent)
			var mu sync.Mutex
			seen := map[int]json.RawMessage{}
			var requests [][]int
			largest := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				batch, size := receiveLogUpload(t, r, intent)
				mu.Lock()
				defer mu.Unlock()
				largest = max(largest, size)
				var accepted []int
				for _, raw := range batch {
					var record struct{ Sequence int }
					if json.Unmarshal(raw, &record) != nil || record.Sequence < 1 || record.Sequence > 128 {
						t.Error("invalid record sequence")
					}
					if previous, exists := seen[record.Sequence]; exists && !bytes.Equal(previous, raw) {
						t.Error("changed record on retry")
					}
					seen[record.Sequence] = append(json.RawMessage(nil), raw...)
					accepted = append(accepted, record.Sequence)
				}
				requests = append(requests, accepted)
				if fail && len(requests) == 2 {
					accepted = accepted[:1] // Stored, but not validly acknowledged.
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "accepted": accepted})
			}))
			defer server.Close()
			upload := actionLogUploader(controlplane.NewClient(server.URL), "fixture-only")
			err := upload(context.Background(), intent, entries)
			if fail {
				if err == nil {
					t.Fatal("partial acknowledgement became success")
				}
				if err = upload(context.Background(), intent, entries); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(seen) != 128 || len(requests) < 2 || largest > 160*1024 {
				t.Fatal("incomplete or oversized final flush")
			}
			if fail && (len(requests) < 4 || !reflect.DeepEqual(requests[0], requests[2]) || !reflect.DeepEqual(requests[1], requests[3])) {
				t.Fatal("retry skipped or changed the partially delivered prefix")
			}
			after, _ := json.Marshal(intent)
			if !bytes.Equal(before, after) {
				t.Fatal("diagnostics changed execution intent")
			}
			t.Logf("delivered %d unique records in %d requests; largest wire body %d bytes", len(seen), len(requests), largest)
		})
	}
}

func TestActionLogUploaderPreservesCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := actionLogUploader(controlplane.NewClient("http://127.0.0.1:1"), "fixture-only")(ctx, logUploadIntent(), escapedLogBacklog(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("diagnostic delivery replaced the caller's context: %v", err)
	}
}
