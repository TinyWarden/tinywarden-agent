package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The server grants 90s using its clock. Small clock differences must not make
// a valid claim fail before collection begins; the local budget stays bounded.
func TestManualClaimDeadlineClockTolerance(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		allowed   bool
	}{
		{"server slightly ahead", 90500 * time.Millisecond, true},
		{"near tolerance", 94500 * time.Millisecond, true},
		{"short server lease", 30 * time.Second, true},
		{"expired", -time.Second, false},
		{"excessive future", 96 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				body["run_deadline"] = baselineStamp(time.Now().Add(tc.remaining))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			lane := packageFixture(t, server)
			entry := prepareManual(lane, time.Now())
			active, err := lane.allocate(entry, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			deadline, err := lane.client.startPackageManual(context.Background(), lane.state, active.Run)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
			if err == nil {
				if deadline.After(time.Now().Add(90 * time.Second)) {
					t.Fatal("clock tolerance widened local execution budget")
				}
				if tc.remaining == 30*time.Second && deadline.After(time.Now().Add(31*time.Second)) {
					t.Fatal("short lease extended")
				}
			}
		})
	}
}
