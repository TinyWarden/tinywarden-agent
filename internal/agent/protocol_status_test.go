package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The declared length is larger than the sent body, so reading it fails after
// the status and headers are already available to the client.
func truncatedResponse(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Length", "200")
	w.WriteHeader(status)
	fmt.Fprint(w, `{"schema_version":1,`)
}

func TestInterruptedErrorBodiesKeepHTTPStatus(t *testing.T) {
	cases := []struct {
		status   int
		category string
	}{
		{400, "server_400"}, {401, "unauthorized"}, {403, "server_403"},
		{404, "server_404"}, {409, "conflict"}, {413, "server_413"},
		{415, "server_415"},
	}
	for _, test := range cases {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				truncatedResponse(w, test.status)
			}))
			defer server.Close()
			_, err := testClient(server).Enroll(context.Background(), testPending(t))
			var response *ResponseError
			if !errors.As(err, &response) || response.Status != test.status ||
				!terminal(err) || ErrorCategory(err) != test.category {
				t.Fatalf("interrupted rejection lost its status/category: %v", err)
			}
		})
	}
}

func TestKnownInvalidSuccessMetadataStopsBeforeBodyRead(t *testing.T) {
	cases := []struct {
		name, media, encoding string
		status                int
		allowCreated          bool
	}{
		{"media", "text/html", "", 200, true},
		{"encoding", "application/json", "gzip", 200, true},
		{"unsupported status", "application/json", "", 202, true},
		{"unexpected created", "application/json", "", 201, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.media)
				if test.encoding != "" {
					w.Header().Set("Content-Encoding", test.encoding)
				}
				truncatedResponse(w, test.status)
			}))
			defer server.Close()
			client := testClient(server)
			client.HTTP.Transport.(*http.Transport).DisableCompression = true
			_, err := client.post(context.Background(), "/api/v1/agent/heartbeat", "test",
				map[string]any{"schema_version": 1}, test.allowCreated)
			if err == nil || !terminal(err) {
				t.Fatalf("known-invalid response became retryable: %v", err)
			}
		})
	}
}

func TestInterruptedAllowedSuccessAndTransientStatusStayRetryable(t *testing.T) {
	for _, status := range []int{200, 201, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1200")
				truncatedResponse(w, status)
			}))
			defer server.Close()
			_, err := testClient(server).Enroll(context.Background(), testPending(t))
			if err == nil || terminal(err) {
				t.Fatalf("interrupted/rejected request became terminal: %v", err)
			}
			if status == 429 || status == 503 {
				var response *ResponseError
				if !errors.As(err, &response) || response.Status != status ||
					response.RetryAfter != 900*time.Second {
					t.Fatalf("transient status/delay lost: %v", err)
				}
			} else {
				var interrupted *responseReadError
				if !errors.As(err, &interrupted) {
					t.Fatalf("successful response body was not retryable: %v", err)
				}
			}
		})
	}
}
