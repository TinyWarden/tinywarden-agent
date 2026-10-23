package legacy

import (
	"encoding/json"
	"slices"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	baseline "github.com/TinyWarden/tinywarden-agent/internal/legacy/checks"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
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
	Scope       agentstate.Scope `json:"scope"`
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
	Scope  agentstate.Scope `json:"scope"`
	Last   uint64           `json:"last"`
	Active *baselineActive  `json:"active"`
}

func validBaselineRequest(r baselineRunRequest) bool {
	n, _ := baseline.Versions(r.Observation.Key)
	return r.SchemaVersion == 1 && agentstate.ValidID(r.ID) && agentstate.ValidID(r.AssignmentID) && r.Sequence >= 1 && r.Sequence <= agentstate.MaxSafeCounter &&
		r.DroppedRuns <= agentstate.MaxSafeCounter && agentstate.ValidInstant(r.StartedAt) && agentstate.ValidInstant(r.FinishedAt) && r.StartedAt <= r.FinishedAt &&
		r.Observation.SchemaVersion == 1 && n != "" && r.Observation.Normalizer == n
}
func validBaselineWork(q baselineQueue, sequence baselineSequence) bool {
	if !q.Scope.Valid() || q.Scope != sequence.Scope || sequence.Last > agentstate.MaxSafeCounter || q.DroppedRuns > agentstate.MaxSafeCounter || len(q.Pending) > 100 || q.Pending == nil {
		return false
	}
	if a := sequence.Active; a != nil {
		if !agentstate.ValidID(a.ID) || a.Sequence != sequence.Last || a.Sequence == 0 || !agentstate.ValidInstant(a.StartedAt) || a.DroppedRuns > agentstate.MaxSafeCounter || !validBaselineEntry(q.Scope, a.Entry) {
			return false
		}
	}
	ids := map[string]bool{}
	seqs := map[uint64]bool{}
	for i, item := range q.Pending {
		var r baselineRunRequest
		if len(item.Body) > 32<<10 || controlplane.Decode([]byte(item.Body), &r) != nil || !validBaselineRequest(r) || item.ID != r.ID || item.Sequence != r.Sequence ||
			item.Sequence > sequence.Last || controlplane.BodyDigest([]byte(item.Body)) != item.Digest || ids[item.ID] || seqs[item.Sequence] ||
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

func loadBaselineWork(store *agentstate.Store, scope agentstate.Scope, cacheExists bool) (baselineQueue, baselineSequence, error) {
	var q baselineQueue
	var s baselineSequence
	qFound, err := controlplane.ReadState(store, "baseline-queue.json", baselineQueueBytes, &q)
	if err != nil {
		return q, s, err
	}
	sFound, err := controlplane.ReadState(store, "baseline-sequence.json", baselineBytes, &s)
	if err != nil {
		return q, s, err
	}
	if qFound && sFound {
		if !validBaselineWork(q, s) {
			return q, s, controlplane.ErrInvalidState
		}
		if q.Scope == scope {
			return q, s, nil
		}
		if err := agentstate.ArchiveState(store, "baseline-queue.json"); err != nil {
			return q, s, err
		}
		if err := agentstate.ArchiveState(store, "baseline-sequence.json"); err != nil {
			return q, s, err
		}
	} else if qFound || sFound || cacheExists {
		return q, s, controlplane.ErrInvalidState
	}
	q = baselineQueue{Scope: scope, Pending: []baselineQueued{}}
	s = baselineSequence{Scope: scope}
	if err := agentstate.SaveState(store, "baseline-sequence.json", s, baselineBytes); err != nil {
		return q, s, err
	}
	return q, s, agentstate.SaveState(store, "baseline-queue.json", q, baselineQueueBytes)
}

// Main loop alone mutates disk state. Persist identity before launching any command.
func allocateBaseline(store *agentstate.Store, s *baselineSequence, entry baselineEntry, started string, dropped uint64) (*baselineActive, error) {
	if s.Active != nil || s.Last >= agentstate.MaxSafeCounter || !validBaselineEntry(s.Scope, entry) || !agentstate.ValidInstant(started) || dropped > agentstate.MaxSafeCounter {
		return nil, controlplane.ErrInvalidState
	}
	id, err := agentstate.RandomID()
	if err != nil {
		return nil, err
	}
	copy := *s
	copy.Last++
	copy.Active = &baselineActive{id, copy.Last, entry, started, dropped}
	if err := agentstate.SaveState(store, "baseline-sequence.json", copy, baselineBytes); err != nil {
		return nil, err
	}
	*s = copy
	return copy.Active, nil
}

func queueBaseline(store *agentstate.Store, q *baselineQueue, r baselineRunRequest) error {
	if !validBaselineRequest(r) {
		return controlplane.ErrInvalidState
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > 32<<10 {
		return controlplane.ErrInvalidState
	}
	item := baselineQueued{r.ID, r.Sequence, controlplane.BodyDigest(data), string(data)}
	copy := *q
	copy.Pending = append(slices.Clone(q.Pending), item)
	for {
		data, err := json.Marshal(copy)
		if err == nil && len(copy.Pending) <= 100 && len(data) <= baselineQueueBytes {
			break
		}
		i := slices.IndexFunc(copy.Pending, func(old baselineQueued) bool { return old.ID != copy.InFlightID && old.ID != item.ID })
		if i < 0 {
			return controlplane.ErrInvalidState
		}
		copy.Pending = append(copy.Pending[:i], copy.Pending[i+1:]...)
		if copy.DroppedRuns < agentstate.MaxSafeCounter {
			copy.DroppedRuns++
		}
	}
	if err := agentstate.SaveState(store, "baseline-queue.json", copy, baselineQueueBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}

func finishBaseline(store *agentstate.Store, q *baselineQueue, s *baselineSequence, r baselineRunRequest) error {
	if s.Active == nil || s.Active.ID != r.ID || s.Active.Sequence != r.Sequence {
		return controlplane.ErrInvalidState
	}
	// Restart after queue persistence must clear the same active identity, not duplicate it.
	if !slices.ContainsFunc(q.Pending, func(item baselineQueued) bool { return item.ID == r.ID }) {
		if err := queueBaseline(store, q, r); err != nil {
			return err
		}
	}
	copy := *s
	copy.Active = nil
	if err := agentstate.SaveState(store, "baseline-sequence.json", copy, baselineBytes); err != nil {
		return err
	}
	*s = copy
	return nil
}

func flightBaseline(store *agentstate.Store, q *baselineQueue, id string, acknowledge bool) error {
	if len(q.Pending) == 0 || q.Pending[0].ID != id || q.InFlightID != "" && q.InFlightID != id || acknowledge && q.InFlightID != id {
		return controlplane.ErrInvalidState
	}
	copy := *q
	copy.InFlightID = id
	if acknowledge {
		copy.Pending = append([]baselineQueued{}, q.Pending[1:]...)
		copy.InFlightID = ""
	}
	if err := agentstate.SaveState(store, "baseline-queue.json", copy, baselineQueueBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}
