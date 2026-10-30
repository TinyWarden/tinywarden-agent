package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func TestMonitoringHintDiscoversClaimsCollectsAndDeliversManualResult(t *testing.T) {
	var response []byte
	var waits, uploads atomic.Int32
	stopWait := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/agent/work-wait":
			if waits.Add(1) > 1 {
				select {
				case <-r.Context().Done():
				case <-stopWait:
				}
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "available": true, "cursor": strings.Repeat("a", 64)})
		case "/api/v2/agent/skill-assignments":
			_, _ = w.Write(response)
		case "/api/v2/agent/skill-run-starts":
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			input["run_deadline"] = controlplane.Timestamp(time.Now().Add(90 * time.Second))
			_ = json.NewEncoder(w).Encode(input)
		case "/api/v2/agent/skill-runs":
			var run packageRun
			_ = json.NewDecoder(r.Body).Decode(&run)
			if run.ManualID == "" || run.Outcome != "observed" {
				t.Error("missing claim or collection")
			}
			uploads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "run_id": run.ID, "run_sequence": run.Sequence, "received_at": controlplane.Timestamp(time.Now())})
		default:
			t.Error("unexpected route", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(stopWait) })
	lane := packageFixture(t, server)
	lane.runtime.Assets = t.TempDir()
	entry := prepareManual(lane, time.Now())
	response, _ = json.Marshal(lane.ledger.Cache)
	if !validPackageResponse(lane.ledger.Scope, *lane.ledger.Cache) {
		t.Fatal("invalid fixture assignment")
	}
	lane.ledger.Cache.Assignments[0].Manual = nil
	lane.ready = func(context.Context) bool { return true }
	lane.waitEnabled = true
	lane.execute = func(_ context.Context, active packageActive) packageCompleted {
		if active.Entry.InstallationID != entry.InstallationID {
			t.Error("wrong installation")
		}
		run := active.Run
		run.Outcome, run.Observation = "observed", json.RawMessage(`{"used":1}`)
		return packageCompleted{run, false}
	}
	started := time.Now()
	settlePackage(t, lane, func() bool { return uploads.Load() == 1 && len(lane.ledger.Pending) == 0 })
	if time.Since(started) > 2*time.Second || lane.ledger.Consumed[entry.InstallationID] != entry.Manual.ID {
		t.Fatal("manual discovery waited for periodic cadence or lost consumption")
	}
	if lane.fetchAgain {
		t.Fatal("hint was not consumed")
	}
	// Stop the optional outstanding wait before httptest.Server.Close.
	lane.stop()
}

func TestMonitoringHintsRespectCooldownAndDoNotLoseInflightNotification(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := packageFixture(t, server)
	now := time.Now()
	lane.waitEnabled, lane.waiting, lane.fetching = true, true, true
	lane.nextWait = now.Add(time.Hour)
	lane.fetchNotBefore, lane.uploadNotBefore = now.Add(time.Minute), now.Add(2*time.Minute)
	lane.work <- packageHint{hint: controlplane.WorkHint{Available: true, Cursor: strings.Repeat("b", 64)}}
	lane.pollHints(now)
	if lane.nextFetch != lane.fetchNotBefore || lane.nextUpload != lane.uploadNotBefore || !lane.fetchAgain {
		t.Fatal("hint bypassed cooldown or lost concurrent fetch")
	}
	// A wait error never postpones ordinary fetch/upload due dates.
	lane.waiting = true
	fetch, upload := lane.nextFetch, lane.nextUpload
	lane.work <- packageHint{err: &controlplane.ResponseError{Status: 404}}
	lane.pollHints(now)
	if lane.nextFetch != fetch || lane.nextUpload != upload || lane.waitFailures != 1 {
		t.Fatal("optional wait failure changed ordinary scheduling")
	}
	// An assignment response already in flight cannot swallow a later hint.
	lane.fetch <- packageFetched{response: lane.ledger.Cache}
	if err := lane.receive(now); err != nil || lane.nextFetch != now || lane.fetchAgain {
		t.Fatal("inflight assignment response swallowed wake-up", err)
	}
}

func TestBlockedMonitoringWaitDoesNotDelayOrdinaryDiscovery(t *testing.T) {
	var response []byte
	var fetched atomic.Int32
	stopWait := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/agent/work-wait" {
			select {
			case <-stopWait:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
		fetched.Add(1)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(stopWait) })
	lane := packageFixture(t, server)
	lane.runtime.Assets = t.TempDir()
	response, _ = json.Marshal(lane.ledger.Cache)
	for _, e := range lane.ledger.Cache.Assignments {
		lane.due[e.InstallationID] = time.Now().Add(time.Hour)
	}
	lane.ready = func(context.Context) bool { return true }
	lane.waitEnabled, lane.nextFetch = true, time.Now()
	settlePackage(t, lane, func() bool { return fetched.Load() == 1 && !lane.fetching })
	lane.nextFetch = time.Now()
	settlePackage(t, lane, func() bool { return fetched.Load() == 2 && !lane.fetching })
	if !lane.waiting {
		t.Fatal("ordinary reads waited for the optional hint connection")
	}
}

func TestManualPriorityPreservesRoundRobinWithinClass(t *testing.T) {
	server := manualServer(t, false)
	defer server.Close()
	lane := packageFixture(t, server)
	now := time.Now()
	entry := prepareManual(lane, now)
	lane.rotate = 1 // Other scheduled entries appear first in rotation.
	for _, e := range lane.ledger.Cache.Assignments {
		lane.due[e.InstallationID] = now.Add(-time.Second)
	}
	if chosen := lane.nextEntry(now); chosen == nil || chosen.InstallationID != entry.InstallationID {
		t.Fatal("periodic work overtook requested check")
	}
	// Same-class rotation resumes after consumption; no duplicate manual claim.
	lane.ledger.Consumed[entry.InstallationID] = entry.Manual.ID
	if chosen := lane.nextEntry(now); chosen == nil || chosen.InstallationID == entry.InstallationID {
		t.Fatal("ordinary round robin lost")
	}
}

func TestMonitoringUploadRecoveryKeepsExactFIFOBytes(t *testing.T) {
	var received [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var run packageRun
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		received = append(received, bytes.Clone(body.Bytes()))
		_ = json.Unmarshal(body.Bytes(), &run)
		if len(received) == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "run_id": run.ID, "run_sequence": run.Sequence, "received_at": controlplane.Timestamp(time.Now())})
	}))
	defer server.Close()
	lane := packageFixture(t, server)
	for _, e := range lane.ledger.Cache.Assignments {
		lane.due[e.InstallationID] = time.Now().Add(time.Hour)
		active, err := lane.allocate(e, time.Now())
		if err != nil || lane.complete(active.Run) != nil {
			t.Fatal("cannot persist pending result", err)
		}
	}
	expected := bytes.Clone(lane.ledger.Pending[0].Body)
	lane.nextUpload = time.Now()
	settlePackage(t, lane, func() bool { return lane.uploadFailures == 1 })
	if time.Until(lane.nextUpload) < 59*time.Second || !bytes.Equal(expected, lane.ledger.Pending[0].Body) {
		t.Fatal("explicit cooldown or original pending bytes lost")
	}
	// Simulate passage of the cooldown; no host clock changes.
	lane.now = func() time.Time { return time.Now().Add(61 * time.Second) }
	settlePackage(t, lane, func() bool { return len(lane.ledger.Pending) == 0 })
	if len(received) != 4 || !bytes.Equal(received[0], received[1]) || !bytes.Equal(received[0], expected) {
		t.Fatal("retry altered or reordered the durable result")
	}
}
