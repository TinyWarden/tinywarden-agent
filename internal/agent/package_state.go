package agent

import (
	"encoding/json"
	"time"
)

func loadPackageLedger(store *Store, scope baselineScope) (packageLedger, error) {
	fresh := packageLedger{Consumed: map[string]string{}, Scope: scope, Sequences: map[string]uint64{}, Pending: []packagePending{}}
	var ledger packageLedger
	found, err := readBaselineState(store, "package-lane.json", packageQueueLimit, &ledger)
	if err != nil {
		return fresh, err
	}
	if !found {
		return fresh, nil
	}
	if ledger.Consumed == nil {
		ledger.Consumed = map[string]string{}
	}
	if !validConsumed(ledger.Consumed) {
		return fresh, errPackageState
	}
	if !ledger.Scope.valid() || ledger.Sequences == nil || ledger.Pending == nil || len(ledger.Sequences) > 100 || len(ledger.Pending) > 8 {
		return fresh, errPackageState
	}
	if ledger.Scope != scope {
		return fresh, savePackageLedger(store, fresh)
	}
	if ledger.Cache != nil && (!validPackageResponse(scope, *ledger.Cache) || !validInstant(ledger.ValidatedAt)) {
		return fresh, errPackageState
	}
	for id, sequence := range ledger.Sequences {
		if !validID(id) || sequence > safeCounter {
			return fresh, errPackageState
		}
	}
	for _, item := range ledger.Pending {
		var run packageRun
		if !validID(item.InstallationID) || len(item.Body) > 1<<20 || baselineBodyDigest(item.Body) != item.Digest || decodeBaseline(item.Body, &run) != nil ||
			!validStoredPackageRun(run) || run.Sequence > ledger.Sequences[item.InstallationID] {
			return fresh, errPackageState
		}
	}
	if ledger.Active != nil {
		active := ledger.Active
		if !validID(active.Entry.InstallationID) || !validStoredPackageRun(active.Run) || active.Run.AssignmentID != active.Entry.ID || active.Run.Sequence != ledger.Sequences[active.Entry.InstallationID] || active.Run.ManualID != "" && (active.Entry.Manual == nil || active.Entry.Manual.ID != active.Run.ManualID || ledger.Consumed[active.Entry.InstallationID] != active.Run.ManualID) {
			return fresh, errPackageState
		}
		active.Run.FinishedAt = active.Run.StartedAt
		active.Run.Outcome = "execution_failed"
		active.Run.Observation = json.RawMessage("null")
		body, err := json.Marshal(active.Run)
		if err != nil {
			return fresh, err
		}
		if len(ledger.Pending) >= 8 {
			return fresh, errPackageState
		}
		ledger.Pending = append(ledger.Pending, packagePending{active.Entry.InstallationID, body, baselineBodyDigest(body)})
		ledger.Active = nil
		if err := savePackageLedger(store, ledger); err != nil {
			return fresh, err
		}
	}
	return ledger, nil
}
func validStoredPackageRun(run packageRun) bool {
	if run.ManualID != "" && !validID(run.ManualID) {
		return false
	}
	if !validID(run.ID) || !validID(run.AssignmentID) || !validInstant(run.StartedAt) || !validInstant(run.FinishedAt) || run.SchemaVersion != 1 || run.Sequence < 1 || run.Sequence > safeCounter {
		return false
	}
	started, _ := time.Parse(time.RFC3339Nano, run.StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, run.FinishedAt)
	if finished.Before(started) || finished.Sub(started) > 65*time.Second || !json.Valid(run.Observation) {
		return false
	}
	if run.Outcome == "observed" {
		return string(run.Observation) != "null"
	}
	switch run.Outcome {
	case "execution_failed", "runtime_unavailable", "capability_denied", "deadline_exceeded", "resource_exhausted", "broker_limit", "output_exceeded", "output_invalid", "observation_unavailable", "package_rejected", "cleanup_failed":
		return string(run.Observation) == "null"
	}
	return false
}
func savePackageLedger(store *Store, ledger packageLedger) error {
	return saveBaselineState(store, "package-lane.json", ledger, packageQueueLimit)
}
func packageLease(ledger packageLedger, now time.Time) bool {
	if ledger.Paused || ledger.Cache == nil || !validInstant(ledger.ValidatedAt) {
		return false
	}
	validated, _ := time.Parse(time.RFC3339Nano, ledger.ValidatedAt)
	issued, _ := time.Parse(time.RFC3339Nano, ledger.Cache.IssuedAt)
	expires, _ := time.Parse(time.RFC3339Nano, ledger.Cache.ValidUntil)
	return !now.Before(validated) && now.Sub(validated) < expires.Sub(issued) && !now.Before(issued.Add(-2*time.Minute)) && now.Before(expires.Add(2*time.Minute))
}
