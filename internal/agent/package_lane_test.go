package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func packageFixture(t *testing.T, server *httptest.Server) *packageLane {
	t.Helper()
	state, _ := baselineFixture(t, server.URL)
	lane, err := newPackageLane(context.Background(), baselineStore(t), testClient(server), state, Config{SkillOperations: []string{}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lane.stop)
	now := time.Now().UTC()
	entries := []packageAssignment{}
	for range 3 {
		id, _ := randomID()
		assignment, _ := randomID()
		entries = append(entries, packageAssignment{InstallationID: id, ID: assignment, Subject: "example/memory", Digest: strings.Repeat("a", 64), Applicability: "ready", Interval: 300, Settings: json.RawMessage(`{"interval_seconds":300}`)})
	}
	lane.ledger.Cache = &packageResponse{1, strconv.FormatUint(state.Generation, 10), baselineStamp(now), baselineStamp(now.Add(5 * time.Minute)), true, entries}
	lane.ledger.ValidatedAt = baselineStamp(now)
	lane.nextFetch, lane.nextUpload = now.Add(time.Hour), now.Add(time.Hour)
	return lane
}
func settlePackage(t *testing.T, lane *packageLane, done func() bool) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for !done() && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
		if err := lane.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !done() {
		t.Fatal("package work did not settle")
	}
}
func TestPackageRestartKeepsIdentityExactBytesAndCredentialScope(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := packageFixture(t, server)
	a, err := lane.allocate(lane.ledger.Cache.Assignments[0], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPackageLedger(lane.store, lane.ledger.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Active != nil || len(loaded.Pending) != 1 {
		t.Fatal("interrupted work lost")
	}
	var run packageRun
	_ = json.Unmarshal(loaded.Pending[0].Body, &run)
	if run.ID != a.Run.ID || run.Sequence != 1 || run.Outcome != "execution_failed" || string(run.Observation) != "null" {
		t.Fatal("restart invented or reused result")
	}
	again, err := loadPackageLedger(lane.store, lane.ledger.Scope)
	if err != nil || !bytes.Equal(again.Pending[0].Body, loaded.Pending[0].Body) {
		t.Fatal("exact replay changed", err)
	}
	changed := lane.ledger.Scope
	changed.Generation++
	fresh, err := loadPackageLedger(lane.store, changed)
	if err != nil || len(fresh.Pending) != 0 || fresh.Cache != nil || len(fresh.Sequences) != 0 {
		t.Fatal("old credential work crossed scope", err)
	}
}
func TestPackageFairWorkerNoCatchupAndBoundedBackpressure(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := packageFixture(t, server)
	started := make(chan string, 3)
	unblock := make(chan struct{}, 3)
	lane.execute = func(ctx context.Context, a packageActive) packageCompleted {
		started <- a.Entry.InstallationID
		select {
		case <-unblock:
		case <-ctx.Done():
		}
		return packageCompleted{a.Run, false}
	}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := lane.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if lane.ledger.Active == nil || len(started) > 1 {
		t.Fatal("queued overlapping work")
	}
	for i := 1; i <= 3; i++ {
		unblock <- struct{}{}
		settlePackage(t, lane, func() bool { return len(lane.ledger.Pending) == i })
	}
	for range 20 {
		_ = lane.tick(context.Background())
	}
	for _, entry := range lane.ledger.Cache.Assignments {
		if <-started != entry.InstallationID || lane.ledger.Sequences[entry.InstallationID] != 1 {
			t.Fatal("unfair or catchup")
		}
	}
	for len(lane.ledger.Pending) < 8 {
		lane.ledger.Pending = append(lane.ledger.Pending, lane.ledger.Pending[0])
	}
	for _, entry := range lane.ledger.Cache.Assignments {
		delete(lane.due, entry.InstallationID)
	}
	if err := lane.tick(context.Background()); err != nil || lane.running {
		t.Fatal("backpressure ignored", err)
	}
}
func TestPackageCleanupFailurePausesDurablyButReceivesAuthority(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := packageFixture(t, server)
	lane.execute = func(ctx context.Context, a packageActive) packageCompleted {
		r := a.Run
		r.Outcome = "cleanup_failed"
		return packageCompleted{r, true}
	}
	_ = lane.tick(context.Background())
	settlePackage(t, lane, func() bool { return lane.ledger.Paused })
	if packageLease(lane.ledger, time.Now()) {
		t.Fatal("paused cleanup permitted execution")
	}
	loaded, err := loadPackageLedger(lane.store, lane.ledger.Scope)
	if err != nil || !loaded.Paused || len(loaded.Pending) != 1 {
		t.Fatal("pause/result lost", err)
	}
	response := *lane.ledger.Cache
	response.Assignments = []packageAssignment{}
	response.RuntimeReady = false
	lane.fetch <- packageFetched{&response, nil}
	if err := lane.receive(time.Now()); err != nil || !lane.ledger.Paused {
		t.Fatal("authority refresh silently resumed", err)
	}
}
