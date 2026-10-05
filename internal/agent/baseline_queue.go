package agent

import (
	"encoding/json"
	"slices"

	baseline "github.com/TinyWarden/tinywarden-agent/internal/skills/builtin"
)

type baselineRunRequest struct {
	SchemaVersion int                  `json:"schema_version"`
	ID            string               `json:"run_id"`
	Sequence      uint64               `json:"run_sequence"`
	AssignmentID  string               `json:"assignment_id"`
	StartedAt     string               `json:"started_at"`
	FinishedAt    string               `json:"finished_at"`
	DroppedRuns   uint64               `json:"dropped_runs"`
	Observation   baseline.Observation `json:"observation"`
}
type baselineQueued struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Digest   string `json:"digest"`
	Body     string `json:"body"`
}
type baselineQueue struct {
	Scope       baselineScope    `json:"scope"`
	DroppedRuns uint64           `json:"dropped_runs"`
	InFlightID  string           `json:"in_flight_id"`
	Pending     []baselineQueued `json:"pending"`
}
type baselineActive struct {
	ID          string        `json:"id"`
	Sequence    uint64        `json:"sequence"`
	Entry       baselineEntry `json:"entry"`
	StartedAt   string        `json:"started_at"`
	DroppedRuns uint64        `json:"dropped_runs"`
}
type baselineSequence struct {
	Scope  baselineScope   `json:"scope"`
	Last   uint64          `json:"last"`
	Active *baselineActive `json:"active"`
}

func validBaselineRequest(r baselineRunRequest) bool {
	n, _ := baseline.Versions(r.Observation.Key)
	return r.SchemaVersion == 1 && validID(r.ID) && validID(r.AssignmentID) && r.Sequence >= 1 && r.Sequence <= safeCounter &&
		r.DroppedRuns <= safeCounter && validInstant(r.StartedAt) && validInstant(r.FinishedAt) && r.StartedAt <= r.FinishedAt &&
		r.Observation.SchemaVersion == 1 && n != "" && r.Observation.Normalizer == n
}
func validBaselineWork(q baselineQueue, sequence baselineSequence) bool {
	if !q.Scope.valid() || q.Scope != sequence.Scope || sequence.Last > safeCounter || q.DroppedRuns > safeCounter || len(q.Pending) > 100 || q.Pending == nil {
		return false
	}
	if a := sequence.Active; a != nil {
		if !validID(a.ID) || a.Sequence != sequence.Last || a.Sequence == 0 || !validInstant(a.StartedAt) || a.DroppedRuns > safeCounter || !validBaselineEntry(q.Scope, a.Entry) {
			return false
		}
	}
	ids := map[string]bool{}
	seqs := map[uint64]bool{}
	for i, item := range q.Pending {
		var r baselineRunRequest
		if len(item.Body) > 32<<10 || decodeBaseline([]byte(item.Body), &r) != nil || !validBaselineRequest(r) || item.ID != r.ID || item.Sequence != r.Sequence ||
			item.Sequence > sequence.Last || baselineBodyDigest([]byte(item.Body)) != item.Digest || ids[item.ID] || seqs[item.Sequence] ||
			q.InFlightID == item.ID && i != 0 {
			return false
		}
		ids[item.ID], seqs[item.Sequence] = true, true
		if a := sequence.Active; a != nil && (item.ID == a.ID || item.Sequence == a.Sequence) &&
			(item.ID != a.ID || item.Sequence != a.Sequence || r.AssignmentID != a.Entry.ID ||
				r.StartedAt != a.StartedAt || r.DroppedRuns != a.DroppedRuns || r.Observation.Key != a.Entry.Key) {
			return false
		}
	}
	if q.InFlightID != "" && (len(q.Pending) == 0 || q.InFlightID != q.Pending[0].ID) {
		return false
	}
	data, err := json.Marshal(q)
	return err == nil && len(data) <= baselineQueueBytes
}

func loadBaselineWork(store *Store, scope baselineScope, cacheExists bool) (baselineQueue, baselineSequence, error) {
	var q baselineQueue
	var s baselineSequence
	qFound, err := readBaselineState(store, "baseline-queue.json", baselineQueueBytes, &q)
	if err != nil {
		return q, s, err
	}
	sFound, err := readBaselineState(store, "baseline-sequence.json", baselineBytes, &s)
	if err != nil {
		return q, s, err
	}
	if qFound && sFound {
		if !validBaselineWork(q, s) {
			return q, s, errBaselineState
		}
		if q.Scope == scope {
			return q, s, nil
		}
		if err := archiveBaselineState(store, "baseline-queue.json"); err != nil {
			return q, s, err
		}
		if err := archiveBaselineState(store, "baseline-sequence.json"); err != nil {
			return q, s, err
		}
	} else if qFound || sFound || cacheExists {
		return q, s, errBaselineState
	}
	q = baselineQueue{Scope: scope, Pending: []baselineQueued{}}
	s = baselineSequence{Scope: scope}
	if err := saveBaselineState(store, "baseline-sequence.json", s, baselineBytes); err != nil {
		return q, s, err
	}
	return q, s, saveBaselineState(store, "baseline-queue.json", q, baselineQueueBytes)
}

// Main loop alone mutates disk state. Persist identity before launching any command.
func allocateBaseline(store *Store, s *baselineSequence, entry baselineEntry, started string, dropped uint64) (*baselineActive, error) {
	if s.Active != nil || s.Last >= safeCounter || !validBaselineEntry(s.Scope, entry) || !validInstant(started) || dropped > safeCounter {
		return nil, errBaselineState
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	copy := *s
	copy.Last++
	copy.Active = &baselineActive{id, copy.Last, entry, started, dropped}
	if err := saveBaselineState(store, "baseline-sequence.json", copy, baselineBytes); err != nil {
		return nil, err
	}
	*s = copy
	return copy.Active, nil
}

func queueBaseline(store *Store, q *baselineQueue, r baselineRunRequest) error {
	if !validBaselineRequest(r) {
		return errBaselineState
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > 32<<10 {
		return errBaselineState
	}
	item := baselineQueued{r.ID, r.Sequence, baselineBodyDigest(data), string(data)}
	copy := *q
	copy.Pending = append(slices.Clone(q.Pending), item)
	for {
		data, err := json.Marshal(copy)
		if err == nil && len(copy.Pending) <= 100 && len(data) <= baselineQueueBytes {
			break
		}
		i := slices.IndexFunc(copy.Pending, func(old baselineQueued) bool { return old.ID != copy.InFlightID && old.ID != item.ID })
		if i < 0 {
			return errBaselineState
		}
		copy.Pending = append(copy.Pending[:i], copy.Pending[i+1:]...)
		if copy.DroppedRuns < safeCounter {
			copy.DroppedRuns++
		}
	}
	if err := saveBaselineState(store, "baseline-queue.json", copy, baselineQueueBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}

func finishBaseline(store *Store, q *baselineQueue, s *baselineSequence, r baselineRunRequest) error {
	if s.Active == nil || s.Active.ID != r.ID || s.Active.Sequence != r.Sequence {
		return errBaselineState
	}
	// Restart after queue persistence must clear the same active identity, not duplicate it.
	if !slices.ContainsFunc(q.Pending, func(item baselineQueued) bool { return item.ID == r.ID }) {
		if err := queueBaseline(store, q, r); err != nil {
			return err
		}
	}
	copy := *s
	copy.Active = nil
	if err := saveBaselineState(store, "baseline-sequence.json", copy, baselineBytes); err != nil {
		return err
	}
	*s = copy
	return nil
}

func flightBaseline(store *Store, q *baselineQueue, id string, acknowledge bool) error {
	if len(q.Pending) == 0 || q.Pending[0].ID != id || q.InFlightID != "" && q.InFlightID != id || acknowledge && q.InFlightID != id {
		return errBaselineState
	}
	copy := *q
	copy.InFlightID = id
	if acknowledge {
		copy.Pending = append([]baselineQueued{}, q.Pending[1:]...)
		copy.InFlightID = ""
	}
	if err := saveBaselineState(store, "baseline-queue.json", copy, baselineQueueBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}
