package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func (client *Client) PostDiskRun(ctx context.Context, credential string, item queuedRun) error {
	wire, err := client.post(ctx, "/api/v1/agent/disk-runs", credential,
		json.RawMessage(item.Body), false, 1024*1024)
	if err != nil {
		return err
	}
	duplicate, valid := boolField(wire, "duplicate")
	_ = duplicate
	if !valid || stringField(wire, "run_id") != item.ID ||
		number(wire, "run_sequence") != int64(item.Sequence) ||
		!validInstant(stringField(wire, "received_at")) {
		return errors.New("invalid run receipt")
	}
	return nil
}

type collectedRun struct {
	request DiskRunRequest
}
type uploadedRun struct {
	id  string
	err error
}
type diskLane struct {
	store          *Store
	client         *Client
	state          *State
	known          **AssignmentCache
	queue          diskQueue
	sequence       diskSequence
	collection     chan collectedRun
	collecting     bool
	upload         chan uploadedRun
	uploading      bool
	nextCollection time.Time
	nextUpload     time.Time
	uploadFailures int
	lastWall       time.Time
	collect        func(context.Context) CollectorResult
	emit           func(string)
}

func newDiskLane(store *Store, client *Client, state *State, known **AssignmentCache,
	emit func(string)) (*diskLane, error) {
	queue, sequence, abandoned, err := loadDiskQueue(store, client.Origin, *state)
	if err != nil {
		return nil, err
	}
	if abandoned {
		emit("disk_queue_abandoned")
	}
	return &diskLane{store: store, client: client, state: state, known: known,
		queue: queue, sequence: sequence, collection: make(chan collectedRun, 1),
		upload: make(chan uploadedRun, 1), emit: emit, collect: runCollector}, nil
}

func stamp() string {
	return time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func (lane *diskLane) tick(ctx context.Context) error {
	select {
	case result := <-lane.collection:
		lane.collecting = false
		request := result.request
		if err := queueRun(lane.store, &lane.queue, request); err != nil {
			// An oversized result is represented by a small failed run with the same identity.
			request.Coverage, request.Reason = "incomplete", "queue_overflow"
			request.Mounts = []DiskMount{}
			request.ExcludedKernel, request.ExcludedRemote = 0, 0
			if err := queueRun(lane.store, &lane.queue, request); err != nil {
				return err
			}
		}
	case result := <-lane.upload:
		lane.uploading = false
		if result.err == nil {
			if err := acknowledgeRun(lane.store, &lane.queue, result.id); err != nil {
				return err
			}
			lane.uploadFailures = 0
			lane.nextUpload = time.Time{}
		} else {
			if terminal(result.err) {
				return result.err
			}
			lane.uploadFailures++
			base := 2 << min(lane.uploadFailures-1, 3)
			lane.nextUpload = time.Now().Add(retryWait(result.err, time.Duration(base)*time.Second))
			if lane.uploadFailures > 4 {
				lane.nextUpload = time.Now().Add(degradedWait(result.err))
			}
			lane.emit("disk_upload_deferred")
		}
	default:
	}
	now := time.Now()
	if !lane.lastWall.IsZero() && now.Before(lane.lastWall) {
		*lane.known = nil
		lane.emit("assignment_lease_invalid")
	}
	lane.lastWall = now
	if !lane.collecting && !now.Before(lane.nextCollection) && assignmentLease(*lane.known, now) {
		sequence, err := allocateRunSequence(lane.store, &lane.sequence)
		if err != nil {
			return err
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		assignment := **lane.known
		dropped := lane.queue.DroppedRuns
		lane.collecting = true
		lane.nextCollection = now.Add(time.Duration(assignment.Assignment.Effective.IntervalSeconds) * time.Second)
		go func() {
			started := stamp()
			result := lane.collect(ctx)
			finished := stamp()
			lane.collection <- collectedRun{request: DiskRunRequest{SchemaVersion: 1,
				RunID: id, RunSequence: sequence, AssignmentID: assignment.ID,
				StartedAt: started, FinishedAt: finished, Coverage: result.Coverage,
				Reason: result.Reason, ExcludedKernel: result.ExcludedKernel,
				ExcludedRemote: result.ExcludedRemote, DroppedRuns: dropped,
				Mounts: result.Mounts}}
		}()
	}
	if !lane.uploading && len(lane.queue.Pending) > 0 && !now.Before(lane.nextUpload) {
		item := lane.queue.Pending[0]
		if lane.queue.InFlightID != item.ID {
			if err := markInFlight(lane.store, &lane.queue, item.ID); err != nil {
				return err
			}
		}
		lane.uploading = true
		go func() {
			lane.upload <- uploadedRun{id: item.ID,
				err: lane.client.PostDiskRun(ctx, lane.state.Credential, item)}
		}()
	}
	return nil
}
