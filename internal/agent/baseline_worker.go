package agent

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/TinyWarden/tinywarden/agent/internal/baseline"
	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

func baselineCapable() bool {
	_, osID, version, arch, err := metadata()
	return err == nil && osID == "debian" && version == "13" && arch == "amd64" && runtime.GOOS == "linux" && os.Geteuid() != 0
}
func baselineCapabilities() []string {
	if baselineCapable() {
		return []string{runner.Capability}
	}
	return []string{}
}
func baselineBoot() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
func baselineStamp(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func interruptedBaseline(a baselineActive, now time.Time) baselineRunRequest {
	n, _ := baseline.Versions(a.Entry.Key)
	finish := baselineStamp(now)
	if finish < a.StartedAt {
		finish = a.StartedAt
	}
	return baselineRunRequest{1, a.ID, a.Sequence, a.Entry.ID, a.StartedAt, finish, a.DroppedRuns,
		baseline.Observation{SchemaVersion: 1, Key: a.Entry.Key, Normalizer: n, Problem: "execution_missing", Execution: []baseline.Execution{}}}
}

func executeBaseline(ctx context.Context, a baselineActive) baselineRunRequest {
	r := interruptedBaseline(a, time.Now())
	if !baselineCapable() {
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
	r.FinishedAt = baselineStamp(after)
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
