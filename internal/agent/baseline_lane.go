package agent

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/runner"
)

type baselineFetched struct {
	response *baselineResponse
	err      error
}
type baselineLane struct {
	store                                 *Store
	client                                *Client
	state                                 State // Captured identity; heartbeat cannot race its credential reads.
	cache                                 *baselineCache
	queue                                 baselineQueue
	sequence                              baselineSequence
	fetch                                 chan baselineFetched
	run                                   chan baselineRunRequest
	upload                                chan uploadedRun
	fetching, running, uploading, paused  bool
	nextFetch, nextUpload                 time.Time
	due                                   [3]time.Time
	rotate, fetchFailures, uploadFailures int
	lastWall                              time.Time
	cancelRun                             context.CancelFunc
	cancel                                context.CancelFunc
	ctx                                   context.Context
	emit                                  func(string)
	execute                               func(context.Context, baselineActive) baselineRunRequest
	available                             func() bool
	capabilities                          func() []string
	now                                   func() time.Time
}

func newBaselineLane(ctx context.Context, store *Store, client *Client, state State, emit func(string)) (*baselineLane, error) {
	scope := scopeBaseline(client.Origin, state)
	cache, err := loadBaselineCache(store, scope)
	if err != nil {
		return nil, err
	}
	queue, sequence, err := loadBaselineWork(store, scope, cache != nil)
	if err != nil {
		return nil, err
	}
	if cache == nil && sequence.Last != 0 {
		return nil, errBaselineState
	}
	paused, err := loadBaselinePause(store, scope)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	lane := &baselineLane{store: store, client: client, state: state, cache: cache, queue: queue, sequence: sequence,
		fetch: make(chan baselineFetched, 1), run: make(chan baselineRunRequest, 1), upload: make(chan uploadedRun, 1),
		ctx: ctx, cancel: cancel, emit: emit, execute: executeBaseline, available: runner.Available, capabilities: baselineCapabilities, now: time.Now,
		paused: paused || cache != nil && cache.Paused}
	if cache != nil {
		// Keep the known wall-time boundary across restart before a refresh can replace it.
		lane.lastWall, _ = time.Parse("2006-01-02T15:04:05.000Z", cache.ValidatedAt)
	}
	if sequence.Active != nil {
		if err := finishBaseline(store, &lane.queue, &lane.sequence, interruptedBaseline(*sequence.Active, time.Now())); err != nil {
			cancel()
			return nil, err
		}
	}
	return lane, nil
}

type baselinePause struct {
	Scope  baselineScope `json:"scope"`
	Paused bool          `json:"paused"`
}

func loadBaselinePause(store *Store, scope baselineScope) (bool, error) {
	var marker baselinePause
	found, err := readBaselineState(store, "baseline-pause.json", 2048, &marker)
	if err != nil || !found {
		return false, err
	}
	if !marker.Scope.valid() || !marker.Paused {
		return false, errBaselineState
	}
	if marker.Scope != scope {
		return false, archiveBaselineState(store, "baseline-pause.json")
	}
	return true, nil
}
func (lane *baselineLane) pause() error {
	lane.paused = true
	if lane.cancelRun != nil {
		lane.cancelRun()
	}
	if err := saveBaselineState(lane.store, "baseline-pause.json", baselinePause{lane.sequence.Scope, true}, 2048); err != nil {
		return err
	}
	return pauseBaseline(lane.store, &lane.cache)
}
func baselineRetry(err error, failures int) time.Duration {
	if failures > 4 {
		return degradedWait(err)
	}
	return retryWait(err, time.Duration(2<<min(failures-1, 3))*time.Second)
}

// Every mutation is owned by this tick. Network/runner workers return one bounded
// result on private buffered channels; none can mutate cache, queue or sequence.
func (lane *baselineLane) tick(context.Context) error {
	now := lane.now()
	wall := now.UTC() // Strip monotonic time before comparing wall-clock changes.
	if !lane.lastWall.IsZero() && wall.Before(lane.lastWall) {
		lane.emit("baseline_lease_invalid")
		if err := lane.pause(); err != nil {
			return err
		}
	}
	lane.lastWall = wall
	if err := lane.receive(now); err != nil {
		return err
	}
	if lane.paused {
		return nil
	}
	expires, leased := baselineLease(lane.cache, wall)
	if !leased && lane.cancelRun != nil {
		lane.cancelRun()
	}
	if !lane.fetching && !now.Before(lane.nextFetch) {
		lane.fetching = true
		known := lane.cache // Immutable while this request is in flight.
		caps := lane.capabilities()
		go func() {
			r, err := lane.client.fetchBaseline(lane.ctx, lane.state, known, caps)
			lane.fetch <- baselineFetched{r, err}
		}()
	}
	if !lane.running && leased && lane.available() {
		for offset := range 3 {
			i := (lane.rotate + offset) % 3
			entry := lane.cache.Entries[i]
			if entry.Assignment.Applicability != "ready" || now.Before(lane.due[i]) {
				continue
			}
			a, err := allocateBaseline(lane.store, &lane.sequence, entry, baselineStamp(wall), lane.queue.DroppedRuns)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithDeadline(lane.ctx, expires)
			lane.cancelRun, lane.running = cancel, true
			lane.due[i] = now.Add(time.Duration(entry.Assignment.Interval) * time.Second)
			lane.rotate = (i + 1) % 3
			go func() { lane.run <- lane.execute(ctx, *a) }()
			break
		}
	}
	if !lane.uploading && len(lane.queue.Pending) != 0 && !now.Before(lane.nextUpload) {
		item := lane.queue.Pending[0]
		if err := flightBaseline(lane.store, &lane.queue, item.ID, false); err != nil {
			return err
		}
		lane.uploading = true
		go func() {
			lane.upload <- uploadedRun{item.ID, lane.client.postBaselineRun(lane.ctx, lane.state.Credential, item)}
		}()
	}
	return nil
}

// Stop before Store closes; the worker has only a bounded cleanup tail after
// cancellation. Unreturned work retains its durable active identity for restart.
func (lane *baselineLane) stop() {
	lane.cancel()
	if lane.cancelRun != nil {
		lane.cancelRun()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for lane.fetching || lane.running || lane.uploading {
		select {
		case <-lane.fetch:
			lane.fetching = false
		case <-lane.run:
			lane.running = false
		case <-lane.upload:
			lane.uploading = false
		case <-ctx.Done():
			return
		}
	}
}

func (lane *baselineLane) receive(now time.Time) error {
	select {
	case result := <-lane.fetch:
		lane.fetching = false
		if result.err != nil {
			if terminal(result.err) {
				// Persistence failure must not hide the 401 that stops every lane.
				return errors.Join(result.err, lane.pause())
			}
			lane.fetchFailures++
			lane.nextFetch = now.Add(baselineRetry(result.err, lane.fetchFailures))
			lane.emit("baseline_fetch_deferred")
		} else if !lane.paused {
			cache, err := resolveBaselineResponse(lane.client.Origin, lane.state, lane.cache, *result.response)
			if err != nil {
				if saveErr := lane.pause(); saveErr != nil {
					return saveErr
				}
				return err
			}
			cache.ValidatedAt = baselineStamp(now)
			if err := saveBaselineCache(lane.store, cache); err != nil {
				return err
			}
			for i, e := range cache.Entries {
				if lane.cache == nil || e.ID != lane.cache.Entries[i].ID {
					lane.due[i] = time.Time{}
				}
			}
			lane.cache = &cache
			lane.fetchFailures = 0
			lane.nextFetch = now.Add(60 * time.Second)
		}
	default:
	}
	select {
	case result := <-lane.run:
		lane.running = false
		lane.cancelRun()
		lane.cancelRun = nil
		if err := finishBaseline(lane.store, &lane.queue, &lane.sequence, result); err != nil {
			return err
		}
	default:
	}
	select {
	case result := <-lane.upload:
		lane.uploading = false
		if result.err != nil {
			if terminal(result.err) {
				return errors.Join(result.err, lane.pause())
			}
			lane.uploadFailures++
			lane.nextUpload = now.Add(baselineRetry(result.err, lane.uploadFailures))
			lane.emit("baseline_upload_deferred")
		} else {
			if !slices.ContainsFunc(lane.queue.Pending, func(q baselineQueued) bool { return q.ID == result.id }) {
				return errBaselineState
			}
			if err := flightBaseline(lane.store, &lane.queue, result.id, true); err != nil {
				return err
			}
			lane.uploadFailures = 0
			lane.nextUpload = time.Time{}
		}
	default:
	}
	return nil
}
