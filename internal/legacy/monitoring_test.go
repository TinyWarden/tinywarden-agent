package legacy

import (
	"context"
	"testing"
)

func TestPauseCancelsLegacyCollectorsAndResumeRequiresFreshAssignment(t *testing.T) {
	server := rejectBaselineServer()
	defer server.Close()
	baseline := fixtureLane(t, server)
	state := baseline.state
	var known *AssignmentCache
	disk, err := newDiskLane(baselineStore(t), baseline.client, &state, &known, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	epoch, allowed := uint64(2), false
	control := &monitoringControl{func() uint64 { return epoch }, func() bool { return allowed }}
	baselineCancelled, diskCancelled := false, false
	baseline.monitoring, baseline.controlEpoch, baseline.running = control, 1, true
	baseline.cancelRun = func() { baselineCancelled = true }
	disk.monitoring, disk.controlEpoch, disk.collecting = control, 1, true
	disk.cancelCollection = func() { diskCancelled = true }
	defer func() { baseline.running = false }()
	ctx := context.Background()
	if err = baseline.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err = disk.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if !baselineCancelled || !diskCancelled {
		t.Fatal("pause did not cancel legacy collectors")
	}
	baseline.running, disk.collecting = false, false
	epoch, allowed = 3, true
	if err = baseline.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if baseline.running {
		t.Fatal("resumed baseline with old control epoch")
	}
	if err = disk.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if disk.collecting {
		t.Fatal("resumed disk with old control epoch")
	}
}
