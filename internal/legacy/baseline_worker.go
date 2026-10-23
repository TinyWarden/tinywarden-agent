package legacy

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	host "github.com/TinyWarden/tinywarden-agent/internal/host"
	baseline "github.com/TinyWarden/tinywarden-agent/internal/legacy/checks"
	"github.com/TinyWarden/tinywarden-agent/internal/legacy/runner"
	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
)

func baselineCapabilities() []string {
	if host.CanObserve() {
		return []string{runner.Capability, "skill-control.v1"}
	}
	return []string{"skill-control.v1"}
}
func baselineBoot() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
func interruptedBaseline(a baselineActive, now time.Time) baselineRunRequest {
	n, _ := baseline.Versions(a.Entry.Key)
	finish := controlplane.Timestamp(now)
	if finish < a.StartedAt {
		finish = a.StartedAt
	}
	return baselineRunRequest{1, a.ID, a.Sequence, a.Entry.ID, a.StartedAt, finish, a.DroppedRuns,
		baseline.Observation{SchemaVersion: 1, Key: a.Entry.Key, Normalizer: n, Problem: "execution_missing", Execution: []baseline.Execution{}}}
}

func executeBaseline(ctx context.Context, a baselineActive) baselineRunRequest {
	r := interruptedBaseline(a, time.Now())
	release, slotErr := skillruntime.Acquire(ctx)
	if slotErr != nil {
		return r
	}
	defer release()
	if !host.CanObserve() {
		r.Observation.Problem = "execution_policy_rejected"
		return r
	}
	before, boot := time.Now(), baselineBoot()
	encoded, err := json.Marshal(a.Entry.Assignment.Recipe)
	if err != nil {
		r.Observation.Problem = "execution_policy_rejected"
		return r
	}
	result := runner.Execute(ctx, encoded)
	after, bootAfter := time.Now(), baselineBoot()
	r.FinishedAt = controlplane.Timestamp(after)
	if r.FinishedAt < a.StartedAt {
		r.FinishedAt = a.StartedAt
		r.Observation.Problem = "clock_uncertain"
		return r
	}
	o, err := baseline.Normalize(a.Entry.Key, a.Entry.Assignment.Recipe, result,
		baseline.Window{StartedAt: before, FinishedAt: after, BootBefore: boot, BootAfter: bootAfter})
	if err == nil {
		r.Observation = o
	}
	// Only the typed observation crosses the worker channel. Transient buffers die here.
	return r
}
