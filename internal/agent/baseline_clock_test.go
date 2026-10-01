package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBaselinePersistedFutureValidationPausesBeforeRestartRefresh(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	now := time.Now()
	validated := baselineStamp(now.Add(time.Hour))
	lane.cache.ValidatedAt = validated
	if err := saveBaselineCache(lane.store, *lane.cache); err != nil {
		t.Fatal(err)
	}
	lane.stop()
	restarted, err := newBaselineLane(context.Background(), lane.store, lane.client, lane.state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stop()
	restarted.now = func() time.Time { return now }
	restarted.execute = func(ctx context.Context, active baselineActive) baselineRunRequest {
		return interruptedBaseline(active, now)
	}
	restarted.available = func() bool { return true }
	reply := baselineReply(restarted.state, cloneEntries(t, restarted.cache.Entries))
	restarted.fetch <- baselineFetched{response: &reply}
	if err := restarted.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !restarted.paused || !restarted.cache.Paused || restarted.cache.ValidatedAt != validated ||
		restarted.sequence.Last != 0 || restarted.running || restarted.fetching {
		t.Fatal("restart renewed or launched work after a known clock rollback")
	}
	if paused, err := loadBaselinePause(restarted.store, restarted.sequence.Scope); err != nil || !paused {
		t.Fatal("clock pause was not durable", err)
	}
	restarted.stop()
	again, err := newBaselineLane(context.Background(), restarted.store, restarted.client, restarted.state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer again.stop()
	again.now = func() time.Time { return now.Add(2 * time.Hour) }
	if err := again.tick(context.Background()); err != nil || !again.paused || again.fetching ||
		again.sequence.Last != 0 || again.cache.ValidatedAt != validated {
		t.Fatal("a later restart cleared the clock pause", err)
	}
	state := again.state
	state.Generation++
	fresh, err := newBaselineLane(context.Background(), again.store, again.client, state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.stop()
	if fresh.paused || fresh.cache != nil || fresh.sequence.Last != 0 || !fresh.lastWall.IsZero() {
		t.Fatal("new generation retained the old clock authority")
	}
	for _, name := range []string{"baseline-assignments.json", "baseline-pause.json"} {
		if _, err := os.Stat(filepath.Join(fresh.store.dir, name)); !os.IsNotExist(err) {
			t.Fatal("old state was not archived", name, err)
		}
		archived, err := filepath.Glob(filepath.Join(fresh.store.dir, name+".abandoned-*"))
		if err != nil || len(archived) != 1 {
			t.Fatal("old clock evidence was lost", name, archived, err)
		}
	}
	fresh.now = func() time.Time { return now }
	fresh.available = func() bool { return true }
	fresh.execute = func(ctx context.Context, active baselineActive) baselineRunRequest {
		<-ctx.Done()
		return interruptedBaseline(active, now)
	}
	entries := cloneEntries(t, again.cache.Entries)
	for i := range entries {
		entries[i].ID, _ = randomID()
		entries[i].Digest = baselineDigest(fresh.sequence.Scope, entries[i])
	}
	delivery := baselineReply(fresh.state, entries)
	fresh.fetch <- baselineFetched{response: &delivery}
	if err := fresh.tick(context.Background()); err != nil || fresh.paused ||
		fresh.cache == nil || fresh.cache.ValidatedAt != baselineStamp(now) || !fresh.running {
		t.Fatal("explicit new generation could not accept fresh authority", err)
	}
}

func TestBaselineNormalRestartStillRenewsAndExecutes(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	now := time.Now()
	lane.cache.ValidatedAt = baselineStamp(now.Add(-time.Hour))
	if err := saveBaselineCache(lane.store, *lane.cache); err != nil {
		t.Fatal(err)
	}
	lane.stop()
	restarted, err := newBaselineLane(context.Background(), lane.store, lane.client, lane.state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stop()
	restarted.now = func() time.Time { return now }
	restarted.available = func() bool { return true }
	restarted.execute = func(ctx context.Context, active baselineActive) baselineRunRequest {
		<-ctx.Done()
		return interruptedBaseline(active, now)
	}
	reply := baselineReply(restarted.state, cloneEntries(t, restarted.cache.Entries))
	restarted.fetch <- baselineFetched{response: &reply}
	if err := restarted.tick(context.Background()); err != nil || restarted.paused ||
		restarted.cache.ValidatedAt != baselineStamp(now) || restarted.sequence.Last != 1 || !restarted.running {
		t.Fatal("normal restart lost renewal or recipe execution", err)
	}
}
