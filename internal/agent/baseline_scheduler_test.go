package agent

import (
	"context"
	"testing"
	"time"
)

func TestBaselineLongWorkerKeepsHeartbeatAndDiskIndependent(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	lane.execute = func(ctx context.Context, a baselineActive) baselineRunRequest {
		<-ctx.Done()
		return interruptedBaseline(a, time.Now())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2300*time.Millisecond)
	defer cancel()
	hb, disk := 0, 0
	err := runLanes(ctx, 1, func(context.Context) error { hb++; return nil }, func(context.Context) error { return nil }, func(string) {},
		func(context.Context) error { disk++; return nil }, lane.tick)
	if err != context.DeadlineExceeded || hb < 2 || disk < 2 || lane.sequence.Last != 1 {
		t.Fatal("slow baseline blocked contact/disk", err, hb, disk, lane.sequence.Last)
	}
	lane.stop()
}

func TestBaselineNewDeliveryDoesNotRewriteActiveRecipe(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	lane := fixtureLane(t, server)
	active := make(chan baselineActive, 2)
	unblock := make(chan struct{})
	lane.execute = func(ctx context.Context, a baselineActive) baselineRunRequest {
		active <- a
		select {
		case <-unblock:
		case <-ctx.Done():
		}
		return interruptedBaseline(a, time.Now())
	}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := <-active
	entries := cloneEntries(t, lane.cache.Entries)
	entries[0].ID, _ = randomID()
	entries[0].Revision++
	entries[0].Assignment.DefinitionRevision++
	entries[0].Assignment.Recipe.Steps[0].Argv = []string{"--simulate", "upgrade"}
	entries[0].Digest = baselineDigest(lane.cache.Scope, entries[0])
	reply := baselineReply(lane.state, entries)
	lane.fetching = true
	lane.fetch <- baselineFetched{response: &reply}
	if err := lane.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lane.cache.Entries[0].ID == old.Entry.ID || lane.sequence.Active.Entry.ID != old.Entry.ID || lane.sequence.Last != 1 {
		t.Fatal("central edit rewrote active snapshot")
	}
	unblock <- struct{}{}
	settleBaseline(t, lane, func() bool { return len(lane.queue.Pending) == 1 })
	if lane.queue.Pending[0].ID != old.ID {
		t.Fatal("result lost original run identity")
	}
	newActive := <-active
	if newActive.Entry.Key == old.Entry.Key && newActive.Entry.ID == old.Entry.ID {
		t.Fatal("scheduler ignored new delivery")
	}
	lane.stop()
}
