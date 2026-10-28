package installation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func TestWorkWaitUsesBoundedHintWithoutAnExecutionGrant(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "ready"}[available], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v2/agent/work-wait" || r.Header.Get("Authorization") != "Bearer test-credential" {
					t.Error("unexpected work-wait request")
				}
				var input map[string]any
				if json.NewDecoder(r.Body).Decode(&input) != nil || len(input) != 3 || input["schema_version"] != float64(1) || input["wait_seconds"] != float64(25) {
					t.Error("unexpected wait body")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "available": available, "cursor": strings.Repeat("a", 64)})
			}))
			defer server.Close()
			if _, err := receiveWorkHint(context.Background(), controlplane.NewClient(server.URL), "test-credential", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type workHintRoundTrip func(*http.Request) (*http.Response, error)

func (f workHintRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func hintReply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestWorkWaitFailuresFallBackToAuthenticatedJobPolling(t *testing.T) {
	for _, failure := range []string{"unavailable", "old_app", "timeout", "malformed"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := controlplane.NewClient("https://control.invalid")
				calls := []string{}
				client.HTTP.Transport = workHintRoundTrip(func(r *http.Request) (*http.Response, error) {
					calls = append(calls, r.URL.Path)
					if r.Header.Get("Authorization") != "Bearer test-credential" {
						t.Fatal("missing ordinary authentication")
					}
					if r.URL.Path == "/api/v1/agent/maintenance-jobs" {
						return hintReply(200, `{"schema_version":1,"job":null}`), nil
					}
					switch failure {
					case "unavailable":
						return hintReply(503, "unavailable"), nil
					case "old_app":
						return hintReply(404, "not found"), nil
					case "timeout":
						return nil, context.DeadlineExceeded
					default:
						return hintReply(200, `{"schema_version":1,"available":true,"grant":"untrusted"}`), nil
					}
				})
				started := time.Now()
				if err := waitForWorkHint(t.Context(), client, "test-credential", filepath.Join(t.TempDir(), "hint.json")); err != nil {
					t.Fatal(err)
				}
				if time.Since(started) != 30*time.Second {
					t.Fatal("missing bounded failure backoff")
				}
				if _, err := client.Post(t.Context(), "/api/v1/agent/maintenance-jobs", "test-credential", map[string]any{"schema_version": 1}, false, nil); err != nil {
					t.Fatal(err)
				}
				if len(calls) != 2 || calls[1] != "/api/v1/agent/maintenance-jobs" {
					t.Fatalf("ordinary polling did not resume: %v", calls)
				}
			})
		})
	}
}

func TestWorkWaitCacheFaultsAreAdvisoryAndDoNotFollowLinks(t *testing.T) {
	for _, fault := range []string{"invalid", "symlink", "write"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "hint.json")
				target := filepath.Join(root, "untouched")
				if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if fault == "invalid" {
					if err := os.WriteFile(path, []byte(`{"grant":"not a cursor"}`), 0666); err != nil {
						t.Fatal(err)
					}
				}
				if fault == "symlink" {
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
				client := controlplane.NewClient("https://control.invalid")
				calls := 0
				client.HTTP.Transport = workHintRoundTrip(func(r *http.Request) (*http.Response, error) {
					calls++
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					return hintReply(200, `{"schema_version":1,"available":true,"cursor":"`+strings.Repeat("a", 64)+`"}`), nil
				})
				started := time.Now()
				if err := waitForWorkHint(t.Context(), client, "test-credential", path); err != nil {
					t.Fatal(err)
				}
				if time.Since(started) != 30*time.Second || fault != "write" && calls != 0 {
					t.Fatal("cache fault did not use bounded fallback")
				}
				if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
					t.Fatal("hint followed or modified its symlink target")
				}
			})
		})
	}
}

func TestWorkWaitCancellationStillStopsDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		client := controlplane.NewClient("https://control.invalid")
		client.HTTP.Transport = workHintRoundTrip(func(r *http.Request) (*http.Response, error) { return hintReply(503, "unavailable"), nil })
		go func() { time.Sleep(time.Second); cancel() }()
		started := time.Now()
		if err := waitForWorkHint(ctx, client, "test-credential", filepath.Join(t.TempDir(), "hint.json")); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
		if time.Since(started) != time.Second {
			t.Fatal("cancellation waited for the full backoff")
		}
	})
}

func TestWorkWaitMalformedAndUnavailableResponsesBackOffUntilCancellation(t *testing.T) {
	for _, body := range []string{`{"schema_version":1}`, `{"schema_version":2,"available":true}`, `{"schema_version":1,"available":true,"grant":"untrusted"}`} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		if _, err := receiveWorkHint(ctx, controlplane.NewClient(server.URL), "test-credential", nil); err == nil {
			t.Error("invalid hint must not be accepted")
		}
		cancel()
		server.Close()
		if calls != 1 {
			t.Fatalf("unexpected retries: %d", calls)
		}
	}
}
