package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func controlDisk(t *testing.T, cache AssignmentCache, disabled bool) AssignmentCache {
	t.Helper()
	cache.ID, _ = randomID()
	cache.Revision++
	cache.Assignment.Applicability = "ready"
	cache.Assignment.Checks = []AssignedCheck{{Kind: "disk_usage"}}
	if disabled {
		cache.Assignment.Applicability = "disabled"
		cache.Assignment.Checks = []AssignedCheck{}
	}
	data, _ := json.Marshal([]any{1, cache.HostID, cache.AgentID, cache.Generation, "disk-local", cache.Revision, 1, 0, "inherit", cache.Assignment.Applicability, "disk_usage.v1", 1, 1, 85, 95, 300, 10, 900})
	sum := sha256.Sum256(data)
	cache.Digest = hex.EncodeToString(sum[:])
	return cache
}
func TestSkillControlDiskRestartAndImmediateResume(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	store := baselineStore(t)
	state := testAssignmentState(t)
	ready := readyRecoveryCache(t, server.URL, state)
	off := controlDisk(t, ready, true)
	if !validAssignment(off) {
		t.Fatal("disabled assignment rejected")
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAssignment(off); err != nil {
		t.Fatal(err)
	}
	known, err := store.LoadAssignment(server.URL, state)
	if err != nil || known == nil {
		t.Fatal("disabled cache restart", err)
	}
	lane, err := newDiskLane(store, testClient(server), &state, &known, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	lane.collect = func(context.Context) CollectorResult { return failedCollection("collector_failed") }
	lane.nextCollection = time.Now().Add(time.Hour)
	if err := lane.tick(context.Background()); err != nil || lane.collecting || lane.sequence.Last != 0 {
		t.Fatal("Off started work", err)
	}
	on := controlDisk(t, off, false)
	known = &on
	lane.nextCollection = time.Now().Add(time.Hour)
	if err := lane.tick(context.Background()); err != nil || !lane.collecting || lane.sequence.Last != 1 {
		t.Fatal("On did not reset due time", err)
	}
	select {
	case <-lane.collection:
	case <-time.After(time.Second):
		t.Fatal("collection did not finish")
	}
}

func TestSkillControlBaselineRestartAndIndependentResume(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	entries := cloneEntries(t, lane.cache.Entries)
	for i := range entries {
		entries[i].ID, _ = randomID()
		entries[i].Revision++
		entries[i].Assignment.Applicability = "disabled"
		entries[i].Digest = baselineDigest(lane.cache.Scope, entries[i])
	}
	response := baselineReply(lane.state, entries)
	lane.fetch <- baselineFetched{response: &response}
	if err := lane.tick(context.Background()); err != nil || lane.running || lane.sequence.Last != 0 {
		t.Fatal("Off started baseline work", err)
	}
	lane.stop()
	restarted, err := newBaselineLane(context.Background(), lane.store, lane.client, lane.state, func(string) {})
	if err != nil {
		t.Fatal("disabled cache restart", err)
	}
	defer restarted.stop()
	restarted.nextFetch, restarted.nextUpload = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	restarted.execute = func(ctx context.Context, a baselineActive) baselineRunRequest {
		return interruptedBaseline(a, time.Now())
	}
	if err := restarted.tick(context.Background()); err != nil || restarted.running {
		t.Fatal("restart started disabled work", err)
	}
	entries = cloneEntries(t, restarted.cache.Entries)
	entries[0].ID, _ = randomID()
	entries[0].Revision++
	entries[0].Assignment.Applicability = "ready"
	entries[0].Digest = baselineDigest(restarted.cache.Scope, entries[0])
	for i := range restarted.due {
		restarted.due[i] = time.Now().Add(time.Hour)
	}
	response = baselineReply(restarted.state, entries)
	restarted.fetch <- baselineFetched{response: &response}
	if err := restarted.tick(context.Background()); err != nil || !restarted.running || restarted.sequence.Active.Entry.Key != entries[0].Key {
		t.Fatal("enabled skill did not resume independently", err)
	}
	settleBaseline(t, restarted, func() bool { return !restarted.running })
	if restarted.sequence.Last != 1 || len(restarted.queue.Pending) != 1 {
		t.Fatal("disabled skills allocated work")
	}
}

func TestSkillControlOffAllowsAlreadyActiveRunToFinish(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	release := make(chan struct{})
	lane.execute = func(ctx context.Context, a baselineActive) baselineRunRequest {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return interruptedBaseline(a, time.Now())
	}
	if err := lane.tick(context.Background()); err != nil || !lane.running {
		t.Fatal("run did not start", err)
	}
	oldID := lane.sequence.Active.Entry.ID
	entries := cloneEntries(t, lane.cache.Entries)
	for i := range entries {
		entries[i].ID, _ = randomID()
		entries[i].Revision++
		entries[i].Assignment.Applicability = "disabled"
		entries[i].Digest = baselineDigest(lane.cache.Scope, entries[i])
	}
	response := baselineReply(lane.state, entries)
	lane.fetch <- baselineFetched{response: &response}
	if err := lane.tick(context.Background()); err != nil || !lane.running {
		t.Fatal("Off interrupted accepted work", err)
	}
	close(release)
	settleBaseline(t, lane, func() bool { return !lane.running })
	if lane.sequence.Last != 1 || len(lane.queue.Pending) != 1 {
		t.Fatal("Off lost old work or started new work")
	}
	var queued baselineRunRequest
	if err := json.Unmarshal([]byte(lane.queue.Pending[0].Body), &queued); err != nil || queued.AssignmentID != oldID {
		t.Fatal("queued run lost original assignment", err)
	}
}
