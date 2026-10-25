package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActionResponseVersionRequiresExactNegotiatedLane(t *testing.T) {
	for _, path := range []string{"/api/v1/agent/maintenance-jobs", "/api/v1/maintenance-recovery/describe", "/api/v1/maintenance-recovery/accept", "/api/v1/maintenance-recovery/finalize", "/api/v1/maintenance-recovery/unknown", "/api/v1/agent/heartbeat", "/api/v2/agent/skill-assignments"} {
		for _, enabled := range []bool{false, true} {
			for _, version := range []int{1, 2, 3} {
				t.Run(fmt.Sprintf("%s/%t/%d", path, enabled, version), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"schema_version":%d}`, version)
					}))
					defer server.Close()
					ctx := context.WithValue(context.Background(), SkillActionsFeatureContext{}, enabled)
					_, err := NewClient(server.URL).Post(ctx, path, "fixture", map[string]any{}, false, nil)
					accepted := version == 1 || version == 2 && enabled && (path == "/api/v1/agent/maintenance-jobs" || path == "/api/v1/maintenance-recovery/describe" || path == "/api/v1/maintenance-recovery/accept" || path == "/api/v1/maintenance-recovery/finalize")
					if (err == nil) != accepted {
						t.Fatalf("accepted=%t error=%v", accepted, err)
					}
				})
			}
		}
	}
}
