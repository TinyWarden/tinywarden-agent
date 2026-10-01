package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func baselineStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestBaselineCrashIdentityRecoveryAndExactFlight(t *testing.T) {
	store := baselineStore(t)
	state, cache := baselineFixture(t, "https://example.org")
	q, s, err := loadBaselineWork(store, cache.Scope, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveBaselineCache(store, cache); err != nil {
		t.Fatal(err)
	}
	a, err := allocateBaseline(store, &s, cache.Entries[0], baselineStamp(time.Now()), 0)
	if err != nil {
		t.Fatal(err)
	}
	lane, err := newBaselineLane(context.Background(), store, NewClient(cache.Scope.Origin), state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	lane.stop()
	if lane.sequence.Last != a.Sequence || lane.sequence.Active != nil || len(lane.queue.Pending) != 1 {
		t.Fatal("interrupted identity was lost/reused")
	}
	var r baselineRunRequest
	if decodeBaseline([]byte(lane.queue.Pending[0].Body), &r) != nil || r.ID != a.ID || r.Observation.Problem != "execution_missing" {
		t.Fatal("restart invented completed observation")
	}
	q, s = lane.queue, lane.sequence
	if err := flightBaseline(store, &q, r.ID, false); err != nil {
		t.Fatal(err)
	}
	original := q.Pending[0]
	for range 110 {
		a, err := allocateBaseline(store, &s, cache.Entries[1], baselineStamp(time.Now()), q.DroppedRuns)
		if err != nil {
			t.Fatal(err)
		}
		if err := finishBaseline(store, &q, &s, interruptedBaseline(*a, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.Pending) != 100 || q.Pending[0] != original || q.DroppedRuns != 11 {
		t.Fatal("cap lost uncertain exact bytes", len(q.Pending), q.DroppedRuns)
	}
	loaded, sequence, err := loadBaselineWork(store, cache.Scope, true)
	if err != nil || loaded.Pending[0] != original || sequence.Last != 111 {
		t.Fatal("durability", err)
	}
	if err := flightBaseline(store, &q, original.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(q.Pending) != 99 || q.InFlightID != "" {
		t.Fatal("ack did not remove exact flight")
	}
}

func TestBaselineQueueThenSequenceCrashIsNotDuplicated(t *testing.T) {
	store := baselineStore(t)
	state, cache := baselineFixture(t, "https://example.org")
	q, s, err := loadBaselineWork(store, cache.Scope, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveBaselineCache(store, cache); err != nil {
		t.Fatal(err)
	}
	a, err := allocateBaseline(store, &s, cache.Entries[0], baselineStamp(time.Now()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := queueBaseline(store, &q, interruptedBaseline(*a, time.Now())); err != nil {
		t.Fatal(err)
	}
	if !validBaselineWork(q, s) {
		t.Fatal("valid queue-before-clear state rejected")
	}
	for _, field := range []string{"run_id", "run_sequence", "assignment_id", "started_at", "dropped_runs"} {
		altered := q
		altered.Pending = append([]baselineQueued{}, q.Pending...)
		var r baselineRunRequest
		if err := decodeBaseline([]byte(altered.Pending[0].Body), &r); err != nil {
			t.Fatal(err)
		}
		switch field {
		case "run_id":
			r.ID = cache.Entries[1].ID
		case "run_sequence":
			r.Sequence++
		case "assignment_id":
			r.AssignmentID = cache.Entries[1].ID
		case "started_at":
			r.StartedAt = baselineStamp(time.Now().Add(-time.Hour))
		case "dropped_runs":
			r.DroppedRuns++
		}
		data, _ := json.Marshal(r)
		altered.Pending[0] = baselineQueued{r.ID, r.Sequence, baselineBodyDigest(data), string(data)}
		if validBaselineWork(altered, s) {
			t.Fatal("inconsistent active/queued identity accepted", field)
		}
	}
	lane, err := newBaselineLane(context.Background(), store, NewClient(cache.Scope.Origin), state, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer lane.stop()
	if len(lane.queue.Pending) != 1 || lane.sequence.Active != nil || lane.sequence.Last != 1 {
		t.Fatal("queue-before-clear duplicated active identity")
	}
}

func TestBaselineCorruptMissingAndUnsafeStateArePreserved(t *testing.T) {
	for _, kind := range []string{"missing-sequence", "corrupt-queue", "duplicate", "symlink", "mode"} {
		t.Run(kind, func(t *testing.T) {
			store := baselineStore(t)
			_, cache := baselineFixture(t, "https://example.org")
			if _, _, err := loadBaselineWork(store, cache.Scope, false); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.dir, "baseline-queue.json")
			switch kind {
			case "missing-sequence":
				os.Remove(filepath.Join(store.dir, "baseline-sequence.json"))
			case "corrupt-queue":
				os.WriteFile(path, []byte("{"), 0600)
			case "duplicate":
				data, _ := os.ReadFile(path)
				os.WriteFile(path, []byte(strings.Replace(string(data), `"dropped_runs":0`, `"dropped_runs":0,"dropped_runs":0`, 1)), 0600)
			case "symlink":
				os.Remove(path)
				os.Symlink(filepath.Join(store.dir, "baseline-sequence.json"), path)
			case "mode":
				os.Chmod(path, 0644)
			}
			before, _ := os.ReadFile(path)
			if _, _, err := loadBaselineWork(store, cache.Scope, false); err == nil {
				t.Fatal("unsafe/missing state reset")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("corrupt state deleted/rewritten")
			}
		})
	}
}

func TestBaselineGenerationArchivesInsteadOfReauthorizing(t *testing.T) {
	store := baselineStore(t)
	state, cache := baselineFixture(t, "https://example.org")
	q, s, err := loadBaselineWork(store, cache.Scope, false)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := allocateBaseline(store, &s, cache.Entries[0], baselineStamp(time.Now()), 0)
	if err := finishBaseline(store, &q, &s, interruptedBaseline(*a, time.Now())); err != nil {
		t.Fatal(err)
	}
	state.Generation++
	newQ, newS, err := loadBaselineWork(store, scopeBaseline(cache.Scope.Origin, state), false)
	if err != nil || len(newQ.Pending) != 0 || newS.Last != 0 {
		t.Fatal("old run replayed under new identity", err)
	}
	paths, _ := filepath.Glob(filepath.Join(store.dir, "baseline-*.abandoned-*"))
	if len(paths) != 2 {
		t.Fatal("abandoned state lost")
	}
	for _, p := range paths {
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0600 {
			t.Fatal("archive permissions")
		}
	}
}

func TestBaselineCombinedByteCapRetainsExactFlight(t *testing.T) {
	store := baselineStore(t)
	_, cache := baselineFixture(t, "https://example.org")
	q, s, err := loadBaselineWork(store, cache.Scope, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 70 {
		a, err := allocateBaseline(store, &s, cache.Entries[0], baselineStamp(time.Now()), q.DroppedRuns)
		if err != nil {
			t.Fatal(err)
		}
		r := interruptedBaseline(*a, time.Now())
		// Test-only bounded padding uses a typed string; the server still rejects it.
		r.Observation.Problem = strings.Repeat("x", 20<<10)
		if err := finishBaseline(store, &q, &s, r); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := flightBaseline(store, &q, a.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, _ := json.Marshal(q)
	if len(data) > baselineQueueBytes || q.DroppedRuns == 0 || len(q.Pending) >= 70 || q.Pending[0].ID != q.InFlightID {
		t.Fatal("combined byte cap failed")
	}
}
