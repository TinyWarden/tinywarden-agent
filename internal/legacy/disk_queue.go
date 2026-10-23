package legacy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	filesystems "github.com/TinyWarden/tinywarden-agent/internal/host/filesystems"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

const maxPendingRuns = 100
const maxPendingBytes = 8 << 20

type DiskRunRequest struct {
	SchemaVersion  int                     `json:"schema_version"`
	RunID          string                  `json:"run_id"`
	RunSequence    uint64                  `json:"run_sequence"`
	AssignmentID   string                  `json:"assignment_id"`
	StartedAt      string                  `json:"started_at"`
	FinishedAt     string                  `json:"finished_at"`
	Coverage       string                  `json:"coverage"`
	Reason         string                  `json:"reason"`
	ExcludedKernel int                     `json:"excluded_kernel"`
	ExcludedRemote int                     `json:"excluded_remote"`
	DroppedRuns    uint64                  `json:"dropped_runs"`
	Mounts         []filesystems.DiskMount `json:"mounts"`
}

type queuedRun struct {
	ID       string          `json:"id"`
	Sequence uint64          `json:"sequence"`
	Digest   string          `json:"digest"`
	Body     json.RawMessage `json:"body"`
}
type diskQueue struct {
	Version     int         `json:"version"`
	Origin      string      `json:"origin"`
	HostID      string      `json:"host_id"`
	AgentID     string      `json:"agent_id"`
	Generation  uint64      `json:"generation"`
	DroppedRuns uint64      `json:"dropped_runs"`
	InFlightID  string      `json:"in_flight_id"`
	Pending     []queuedRun `json:"pending"`
}
type diskSequence struct {
	Version    int    `json:"version"`
	Origin     string `json:"origin"`
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
	Last       uint64 `json:"last"`
}

func newDiskQueue(origin string, state agentstate.State) diskQueue {
	return diskQueue{Version: 1, Origin: origin, HostID: state.HostID, AgentID: state.AgentID,
		Generation: state.Generation, Pending: []queuedRun{}}
}
func (q diskQueue) scoped(origin string, state agentstate.State) bool {
	return q.Version == 1 && q.Origin == origin && q.HostID == state.HostID &&
		q.AgentID == state.AgentID && q.Generation == state.Generation
}

func loadDiskQueue(store *agentstate.Store, origin string, state agentstate.State) (diskQueue, diskSequence, bool, error) {
	var q diskQueue
	qExists, err := agentstate.ReadFile(store, "disk-queue.json", maxPendingBytes, &q)
	if err != nil {
		return q, diskSequence{}, false, err
	}
	var sequence diskSequence
	sExists, err := agentstate.ReadFile(store, "disk-sequence.json", 1024, &sequence)
	if err != nil {
		return q, sequence, false, err
	}
	abandoned := qExists && !q.scoped(origin, state) && len(q.Pending) > 0
	currentQueue := qExists && q.scoped(origin, state)
	if !qExists || !q.scoped(origin, state) {
		q = newDiskQueue(origin, state)
	}
	if !sExists || sequence.Origin != origin || sequence.HostID != state.HostID ||
		sequence.AgentID != state.AgentID || sequence.Generation != state.Generation {
		if currentQueue {
			return q, sequence, abandoned, errors.New("disk sequence missing")
		}
		sequence = diskSequence{Version: 1, Origin: origin, HostID: state.HostID,
			AgentID: state.AgentID, Generation: state.Generation}
		if err := agentstate.WriteFile(store, "disk-sequence.json", sequence, 1024); err != nil {
			return q, sequence, abandoned, err
		}
	}
	if sequence.Version != 1 || sequence.Last > 9007199254740991 ||
		len(q.Pending) > maxPendingRuns || q.DroppedRuns > 9007199254740991 {
		return q, sequence, abandoned, errors.New("invalid disk state")
	}
	bytes := 0
	foundFlight := q.InFlightID == ""
	for _, item := range q.Pending {
		var request DiskRunRequest
		if json.Unmarshal(item.Body, &request) != nil || !agentstate.ValidID(item.ID) ||
			request.RunID != item.ID || request.RunSequence != item.Sequence ||
			item.Sequence > sequence.Last || item.Sequence == 0 {
			return q, sequence, abandoned, errors.New("invalid queued run")
		}
		hash := sha256.Sum256(item.Body)
		if hex.EncodeToString(hash[:]) != item.Digest {
			return q, sequence, abandoned, errors.New("corrupt queued run")
		}
		if item.ID == q.InFlightID {
			foundFlight = true
		}
		bytes += len(item.Body)
	}
	if !foundFlight || bytes > maxPendingBytes {
		return q, sequence, abandoned, errors.New("invalid disk queue")
	}
	return q, sequence, abandoned, nil
}

func allocateRunSequence(store *agentstate.Store, sequence *diskSequence) (uint64, error) {
	if sequence.Last >= 9007199254740991 {
		return 0, errors.New("run sequence exhausted")
	}
	copy := *sequence
	copy.Last++
	if err := agentstate.WriteFile(store, "disk-sequence.json", copy, 1024); err != nil {
		return 0, err
	}
	*sequence = copy
	return copy.Last, nil
}

func queueRun(store *agentstate.Store, q *diskQueue, request DiskRunRequest) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return errors.New("run too large")
	}
	hash := sha256.Sum256(data)
	item := queuedRun{ID: request.RunID, Sequence: request.RunSequence,
		Digest: hex.EncodeToString(hash[:]), Body: json.RawMessage(data)}
	copy := *q
	copy.Pending = append(append([]queuedRun(nil), q.Pending...), item)
	size := func() int {
		data, err := json.Marshal(copy)
		if err != nil {
			return maxPendingBytes + 1
		}
		return len(data)
	}
	for len(copy.Pending) > maxPendingRuns || size() > maxPendingBytes-64 {
		index := -1
		for i, pending := range copy.Pending {
			if pending.ID != copy.InFlightID && pending.ID != item.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("queue full")
		}
		copy.Pending = append(copy.Pending[:index], copy.Pending[index+1:]...)
		if copy.DroppedRuns < 9007199254740991 {
			copy.DroppedRuns++
		}
	}
	if err := agentstate.WriteFile(store, "disk-queue.json", copy, maxPendingBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}

func markInFlight(store *agentstate.Store, q *diskQueue, id string) error {
	if len(q.Pending) == 0 || q.Pending[0].ID != id ||
		(q.InFlightID != "" && q.InFlightID != id) {
		return errors.New("in-flight run is not queue head")
	}
	copy := *q
	copy.InFlightID = id
	if err := agentstate.WriteFile(store, "disk-queue.json", copy, maxPendingBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}
func acknowledgeRun(store *agentstate.Store, q *diskQueue, id string) error {
	if len(q.Pending) == 0 || q.Pending[0].ID != id || q.InFlightID != id {
		return errors.New("acknowledged run is not queue head")
	}
	copy := *q
	copy.Pending = append([]queuedRun(nil), q.Pending[1:]...)
	if copy.InFlightID == id {
		copy.InFlightID = ""
	}
	if err := agentstate.WriteFile(store, "disk-queue.json", copy, maxPendingBytes); err != nil {
		return err
	}
	*q = copy
	return nil
}

func assignmentLease(cache *AssignmentCache, now time.Time) bool {
	if cache == nil || cache.Assignment.Applicability != "ready" || cache.ValidatedAt == "" {
		return false
	}
	validated, err := time.Parse("2006-01-02T15:04:05.000Z", cache.ValidatedAt)
	return err == nil && !now.Before(validated) && now.Sub(validated) < 24*time.Hour
}
