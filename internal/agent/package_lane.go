package agent

import (
	"context"
	"errors"
	"time"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	host "github.com/TinyWarden/tinywarden-agent/internal/host"
	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

type packageFetched struct {
	controlEpoch uint64
	response     *packageResponse
	err          error
}
type packageCompleted struct {
	run   packageRun
	pause bool
}
type packageUploaded struct {
	id  string
	err error
}
type packageLane struct {
	monitoring                            *monitoringLane
	controlEpoch                          uint64
	store                                 *agentstate.Store
	client                                *Client
	state                                 agentstate.State
	config                                agentconfig.Config
	ledger                                packageLedger
	runtime                               skillruntime.Client
	ctx                                   context.Context
	cancel                                context.CancelFunc
	cancelRun                             context.CancelFunc
	fetch                                 chan packageFetched
	run                                   chan packageCompleted
	upload                                chan packageUploaded
	fetching, running, uploading          bool
	nextFetch, nextUpload, lastWall       time.Time
	due                                   map[string]time.Time
	rotate, fetchFailures, uploadFailures int
	emit                                  func(string)
	now                                   func() time.Time
	ready                                 func(context.Context) bool
	execute                               func(context.Context, packageActive) packageCompleted
}

func newPackageLane(ctx context.Context, store *agentstate.Store, client *Client, state agentstate.State, config agentconfig.Config, emit func(string)) (*packageLane, error) {
	ledger, err := loadPackageLedger(store, agentstate.ScopeFor(client.Origin, state))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	if config.RuntimeAssets == "" {
		config.RuntimeAssets = "/usr/local/lib/tinywarden-agent/runtime"
	}
	if config.SkillOperations == nil {
		config.SkillOperations = []string{"files.read", "files.stat", "files.list", "filesystems.snapshot", "systemd.properties", "command.capture", "systemd.services.list", "systemd.services.read"}
	}
	lane := &packageLane{store: store, client: client, state: state, config: config, ledger: ledger,
		runtime: skillruntime.Client{Assets: config.RuntimeAssets}, ctx: ctx, cancel: cancel, emit: emit, now: time.Now,
		due: map[string]time.Time{}, fetch: make(chan packageFetched, 1), run: make(chan packageCompleted, 1), upload: make(chan packageUploaded, 1)}
	lane.ready = func(ctx context.Context) bool { return host.CanObserve() && lane.runtime.Ready(ctx) }
	lane.execute = lane.collect
	if ledger.Cache != nil {
		lane.lastWall, _ = time.Parse(time.RFC3339Nano, ledger.ValidatedAt)
	}
	return lane, nil
}

// The tick alone mutates durable state; workers never access the Store or heartbeat identity.
func (lane *packageLane) tick(context.Context) error {
	now := lane.now()
	wall := now.UTC()
	if !lane.lastWall.IsZero() && wall.Before(lane.lastWall) {
		return lane.pause("package_clock_invalid")
	}
	lane.lastWall = wall
	if err := lane.receive(now); err != nil {
		return err
	}
	leased := packageLease(lane.ledger, wall) && lane.monitoring.Allowed() && lane.controlEpoch == lane.monitoring.Epoch()
	if !leased && lane.cancelRun != nil {
		lane.cancelRun()
	}
	if !lane.fetching && !now.Before(lane.nextFetch) {
		lane.fetching = true
		paused := lane.ledger.Paused
		controlEpoch := lane.monitoring.Epoch()
		go func() {
			r, e := lane.client.fetchPackages(lane.ctx, lane.state, !paused && lane.ready(lane.ctx), !paused && lane.runtime.ResourcesReady(lane.ctx))
			lane.fetch <- packageFetched{controlEpoch: controlEpoch, response: r, err: e}
		}()
	}
	if !lane.running && leased && len(lane.ledger.Pending) < 8 {
		entries := lane.ledger.Cache.Assignments
		for offset := 0; offset < len(entries); offset++ {
			index := (lane.rotate + offset) % len(entries)
			entry := entries[index]
			if entry.Applicability != "ready" || now.Before(lane.due[entry.InstallationID]) && !lane.manualReady(entry, wall) {
				continue
			}
			active, err := lane.allocate(entry, wall)
			if err != nil {
				return err
			}
			expires, _ := time.Parse(time.RFC3339Nano, lane.ledger.Cache.ValidUntil)
			ctx, cancel := context.WithDeadline(lane.ctx, expires)
			lane.cancelRun, lane.running = cancel, true
			if !now.Before(lane.due[entry.InstallationID]) {
				lane.due[entry.InstallationID] = now.Add(time.Duration(entry.Interval) * time.Second)
			}
			lane.rotate = (index + 1) % len(entries)
			go func() { lane.run <- lane.executeActive(ctx, active) }()
			break
		}
	}
	if !lane.uploading && len(lane.ledger.Pending) > 0 && !now.Before(lane.nextUpload) {
		item := lane.ledger.Pending[0]
		lane.uploading = true
		go func() {
			var run packageRun
			_ = controlplane.Decode(item.Body, &run)
			lane.upload <- packageUploaded{run.ID, lane.client.uploadPackage(lane.ctx, lane.state, item)}
		}()
	}
	return nil
}

func (lane *packageLane) pause(reason string) error {
	lane.ledger.Paused = true
	if lane.cancelRun != nil {
		lane.cancelRun()
	}
	lane.emit(reason)
	return savePackageLedger(lane.store, lane.ledger)
}
func (lane *packageLane) stop() {
	lane.cancel()
	if lane.cancelRun != nil {
		lane.cancelRun()
	}
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for lane.fetching || lane.running || lane.uploading {
		select {
		case <-lane.fetch:
			lane.fetching = false
		case <-lane.run:
			lane.running = false
		case <-lane.upload:
			lane.uploading = false
		case <-timer.C:
			return
		}
	}
}

func (lane *packageLane) receive(now time.Time) error {
	select {
	case result := <-lane.fetch:
		lane.fetching = false
		if result.controlEpoch != lane.monitoring.Epoch() {
			lane.nextFetch = now
			return nil
		}
		if result.err != nil {
			if controlplane.Terminal(result.err) {
				return errors.Join(result.err, lane.pause("package_authority_invalid"))
			}
			lane.fetchFailures++
			lane.nextFetch = now.Add(controlplane.WorkerRetry(result.err, lane.fetchFailures))
			lane.emit("package_fetch_deferred")
		} else {
			if lane.ledger.Active != nil && !packageEntryCurrent(result.response, lane.ledger.Active.Entry) && lane.cancelRun != nil {
				lane.cancelRun()
			}
			for _, entry := range result.response.Assignments {
				if lane.ledger.Cache != nil && !packageEntryCurrent(lane.ledger.Cache, entry) {
					delete(lane.due, entry.InstallationID)
				}
			}
			lane.controlEpoch = result.controlEpoch
			lane.ledger.Cache = result.response
			lane.ledger.ValidatedAt = controlplane.Timestamp(now)
			if err := savePackageLedger(lane.store, lane.ledger); err != nil {
				return err
			}
			lane.fetchFailures = 0
			lane.nextFetch = now.Add(time.Minute)
			if !result.response.RuntimeReady {
				lane.emit("package_runtime_unavailable")
			}
		}
	default:
	}
	select {
	case result := <-lane.run:
		lane.running = false
		if result.run.Outcome == "runtime_unavailable" && lane.ledger.Active != nil {
			id := lane.ledger.Active.Entry.InstallationID
			if retry := now.Add(time.Minute); retry.Before(lane.due[id]) {
				lane.due[id] = retry
			}
		}
		if lane.cancelRun != nil {
			lane.cancelRun()
			lane.cancelRun = nil
		}
		if err := lane.complete(result.run); err != nil {
			return err
		}
		if result.pause {
			return lane.pause("package_cleanup_failed")
		}
	default:
	}
	select {
	case result := <-lane.upload:
		lane.uploading = false
		if result.err == nil || packageObsolete(result.err) {
			if len(lane.ledger.Pending) == 0 {
				return errPackageState
			}
			var run packageRun
			if controlplane.Decode(lane.ledger.Pending[0].Body, &run) != nil || run.ID != result.id {
				return errPackageState
			}
			lane.ledger.Pending = lane.ledger.Pending[1:]
			if err := savePackageLedger(lane.store, lane.ledger); err != nil {
				return err
			}
			lane.uploadFailures = 0
			lane.nextUpload = now
			if result.err != nil {
				lane.emit("package_pending_obsolete")
			}
		} else {
			if controlplane.Terminal(result.err) {
				return errors.Join(result.err, lane.pause("package_authority_invalid"))
			}
			lane.uploadFailures++
			lane.nextUpload = now.Add(controlplane.WorkerRetry(result.err, lane.uploadFailures))
			lane.emit("package_upload_deferred")
		}
	default:
	}
	return nil
}
func packageEntryCurrent(response *packageResponse, entry packageAssignment) bool {
	for _, current := range response.Assignments {
		if current.InstallationID == entry.InstallationID && current.ID == entry.ID && current.Digest == entry.Digest && current.Applicability == "ready" {
			return true
		}
	}
	return false
}
func packageObsolete(err error) bool {
	var response *controlplane.ResponseError
	return errors.As(err, &response) && (response.Status == 400 || response.Status == 409 || response.Status == 410)
}
