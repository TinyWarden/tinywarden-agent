package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func (lane *packageLane) allocate(entry packageAssignment, now time.Time) (packageActive, error) {
	if lane.ledger.Active != nil || lane.ledger.Sequences[entry.InstallationID] >= agentstate.MaxSafeCounter {
		return packageActive{}, errPackageState
	}
	id, err := agentstate.RandomID()
	if err != nil {
		return packageActive{}, err
	}
	next := lane.ledger.Sequences[entry.InstallationID] + 1
	active := packageActive{entry, packageRun{SchemaVersion: 1, ID: id, Sequence: next, AssignmentID: entry.ID, StartedAt: controlplane.Timestamp(now), FinishedAt: controlplane.Timestamp(now), Outcome: "execution_failed", Observation: json.RawMessage("null")}}
	if entry.Format == 2 {
		active.Run.SchemaVersion = 2
	}
	if lane.manualReady(entry, now) {
		active.Run.ManualID = entry.Manual.ID
		lane.ledger.Consumed[entry.InstallationID] = entry.Manual.ID
	}
	lane.ledger.Sequences[entry.InstallationID] = next
	lane.ledger.Active = &active
	if err := savePackageLedger(lane.store, lane.ledger); err != nil {
		return packageActive{}, err
	}
	return active, nil
}
func (lane *packageLane) complete(run packageRun) error {
	if lane.ledger.Active == nil || lane.ledger.Active.Run.ID != run.ID || len(lane.ledger.Pending) >= 8 {
		return errPackageState
	}
	body, err := json.Marshal(run)
	if err != nil {
		return err
	}
	// Budget the complete upload, including run identity and manual request.
	// Preserve the allocated identity as a bounded failure instead of persisting
	// a body that can neither be uploaded nor recovered on restart.
	if len(body) > 1<<20 {
		run.Outcome = "output_exceeded"
		run.Observation = json.RawMessage("null")
		run.Resources = nil
		body, err = json.Marshal(run)
		if err != nil {
			return err
		}
	}
	lane.ledger.Pending = append(lane.ledger.Pending, packagePending{lane.ledger.Active.Entry.InstallationID, body, controlplane.BodyDigest(body)})
	lane.ledger.Active = nil
	return savePackageLedger(lane.store, lane.ledger)
}
func (lane *packageLane) collect(ctx context.Context, active packageActive) packageCompleted {
	run := active.Run
	slotCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	release, err := skillruntime.Acquire(slotCtx)
	cancel()
	if err != nil {
		return packageCompleted{run, false}
	}
	defer release()
	run.StartedAt = controlplane.Timestamp(time.Now())
	run.FinishedAt = run.StartedAt
	helper, err := os.Executable()
	if err != nil {
		return packageCompleted{run, false}
	}
	directory, err := lane.ensurePackage(ctx, active.Entry)
	if err != nil {
		run.Outcome = "runtime_unavailable"
		var failure *skillruntime.Error
		if errors.As(err, &failure) && failure.Code == "cleanup_failed" {
			return packageCompleted{run, true}
		}
		return packageCompleted{run, false}
	}
	ceiling := []json.RawMessage{}
	for _, grant := range active.Entry.Grants {
		var parsed struct {
			Operation string `json:"operation"`
		}
		if json.Unmarshal(grant, &parsed) != nil {
			run.Outcome = "capability_denied"
			return packageCompleted{run, false}
		}
		for _, operation := range lane.config.SkillOperations {
			if operation == parsed.Operation {
				ceiling = append(ceiling, grant)
				break
			}
		}
	}
	protected := []string{lane.config.StateDir, lane.config.Path(), lane.config.RuntimeAssets}
	if lane.config.Managed {
		protected = append(protected, filepath.Dir(lane.config.Path()))
	}
	observation, err := lane.runtime.Collect(ctx, skillruntime.Request{Package: directory, ContentSHA256: active.Entry.Digest,
		Official: active.Entry.Official, Arguments: active.Entry.Settings, Grants: active.Entry.Grants, Ceiling: ceiling,
		FilesystemHelper: helper, ProtectedPaths: protected})
	run.FinishedAt = controlplane.Timestamp(time.Now())
	if run.FinishedAt < run.StartedAt {
		run.FinishedAt = run.StartedAt
		run.Outcome = "execution_failed"
		return packageCompleted{run, false}
	}
	if err == nil {
		run.Outcome = "observed"
		if active.Entry.Format == 2 {
			var envelope struct {
				Observation json.RawMessage `json:"observation"`
				Resources   json.RawMessage `json:"resources"`
			}
			if controlplane.Decode(observation, &envelope) != nil || len(envelope.Observation) == 0 || len(envelope.Resources) == 0 {
				run.Outcome = "output_invalid"
				return packageCompleted{run, false}
			}
			run.Observation = envelope.Observation
			run.Resources = envelope.Resources
		} else {
			run.Observation = observation
		}
		return packageCompleted{run, false}
	}
	var failure *skillruntime.Error
	if errors.As(err, &failure) {
		switch failure.Code {
		case "runtime_unavailable", "capability_denied", "deadline_exceeded", "resource_exhausted", "broker_limit", "output_exceeded", "output_invalid", "observation_unavailable", "package_rejected", "cleanup_failed":
			run.Outcome = failure.Code
		}
		return packageCompleted{run, failure.Code == "cleanup_failed"}
	}
	return packageCompleted{run, false}
}
