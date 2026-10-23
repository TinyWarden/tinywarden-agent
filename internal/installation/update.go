package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

type transaction struct {
	Target    string                 `json:"target"`
	Previous  string                 `json:"previous"`
	Phase     string                 `json:"phase"`
	StartedAt string                 `json:"started_at"`
	Receipt   agentrelease.Candidate `json:"receipt"`
}

func record(base string, t transaction) error {
	return atomicFile(filepath.Join(base, "updater", "private", "transaction.json"), t, 0600)
}
func Update(ctx context.Context, base string, automatic, checkOnly bool) error {
	if e := Preflight(base, false); e != nil {
		return e
	}
	guard, e := lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	c, e := config(base)
	if e != nil {
		return e
	}
	pending, e := pendingTransaction(base)
	if e != nil {
		return e
	}
	if pending != nil && pending.Phase != "awaiting_confirmation" {
		return errors.New("interrupted update preserved; run repair manually")
	}
	_, installed, e := current(base)
	if e != nil {
		return e
	}
	prior := agentrelease.UpdateStatus{Outcome: "idle"}
	if b, e := agentrelease.ReadPublic(filepath.Join(base, "updater", "status.json"), 2048); e == nil {
		_ = agentrelease.Decode(b, &prior)
	}
	if automatic {
		if !c.Updates.Automatic {
			return status(base, agentrelease.UpdateStatus{Outcome: "disabled", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		}
		checked, _ := time.Parse(time.RFC3339Nano, prior.CheckedAt)
		if time.Since(checked) < 20*time.Hour {
			return nil
		}
	}
	checked := time.Now().UTC().Format(time.RFC3339Nano)
	p, e := policy(ctx, base, c, installed.Version)
	if e != nil {
		_ = status(base, agentrelease.UpdateStatus{Outcome: "failed", CheckedAt: checked, Error: "app_unreachable"})
		return e
	}
	if prior.Outcome == "awaiting_confirmation" && prior.Target == installed.Version {
		if e = confirm(ctx, base, c, installed); e != nil {
			return e
		}
		if pending != nil {
			pending.Phase = "completed"
			if e = record(base, *pending); e != nil {
				return e
			}
		}
		return cleanup(base, installed.Version)
	}
	if p.Candidate == nil {
		return status(base, agentrelease.UpdateStatus{Outcome: "idle", CheckedAt: checked})
	}
	m, e := agentrelease.Verify([]byte(p.Candidate.Manifest), p.Candidate.Signature)
	if e != nil {
		_ = failStatus(base, "", "release_invalid")
		return e
	}
	decision, e := updateDecision(m, installed.Version, p, c.Updates.Automatic, automatic)
	if e != nil {
		return e
	}
	if decision != "available" {
		if decision == "idle" {
			_ = cleanup(base, installed.Version)
		}
		return status(base, agentrelease.UpdateStatus{Outcome: decision, Target: m.Version, CheckedAt: checked})
	}
	if checkOnly {
		return status(base, agentrelease.UpdateStatus{Outcome: "available", Target: m.Version, CheckedAt: checked})
	}
	if automatic && prior.Outcome == "failed" && prior.Target == m.Version {
		return errors.New("release previously failed; run repair manually")
	}
	if e = status(base, agentrelease.UpdateStatus{Outcome: "installing", CheckedAt: checked, Target: m.Version}); e != nil {
		return e
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = status(base, agentrelease.UpdateStatus{Outcome: "failed", CheckedAt: checked, Target: m.Version, Error: "update_failed"})
		}
	}()
	archive := filepath.Join(base, "updater", "private", "download-"+m.Version+".tar.gz")
	_ = os.Remove(archive)
	defer os.Remove(archive)
	if e = controlplane.Download(ctx, controlplane.HTTP(true), agentrelease.AssetURL(m), archive, m.Asset.Size, m.Asset.SHA256); e != nil {
		return e
	}
	release, e := stage(base, archive, m)
	if e != nil {
		return e
	}
	if e = verifyUnits(ctx, base, release); e != nil {
		return e
	}
	// Recheck direct app policy after downloading. Agent-writable files cannot grant updates.
	fresh, e := policy(ctx, base, c, installed.Version)
	if e = recheck(p, fresh, c.Updates.Automatic, automatic, e); e != nil {
		os.RemoveAll(release)
		return errors.New("update policy changed")
	}
	t := transaction{Target: m.Version, Previous: installed.Version, Phase: "staged", StartedAt: checked, Receipt: *p.Candidate}
	if e = record(base, t); e != nil {
		return e
	}
	// A disabled/masked observation service is an administrator decision.
	if e = system(ctx, "is-enabled", "--quiet", Service); e != nil {
		return errors.New("observation service is disabled or masked")
	}
	if e = system(ctx, "stop", Service); e != nil {
		return e
	}
	t.Phase = "switching"
	if e = record(base, t); e != nil {
		return e
	}
	if e = switchCurrent(base, m.Version); e != nil {
		return e
	}
	if e = atomicFile(filepath.Join(base, "updater", "receipt.json"), *p.Candidate, 0644); e != nil {
		return e
	}
	if e = units(ctx, base, release); e != nil {
		return e
	}
	if e = system(ctx, "start", Service); e != nil {
		return e
	}
	t.Phase = "awaiting_confirmation"
	if e = record(base, t); e != nil {
		return e
	}
	if e = status(base, agentrelease.UpdateStatus{Outcome: "awaiting_confirmation", CheckedAt: checked, Target: m.Version}); e != nil {
		return e
	}
	succeeded = true // Installation is complete; confirmation is a separate outcome.
	if e = confirm(ctx, base, c, m); e != nil {
		return e
	}
	t.Phase = "completed"
	if e = record(base, t); e != nil {
		return e
	}
	return cleanup(base, m.Version)
}
func Repair(ctx context.Context, base string) error {
	if e := Preflight(base, false); e != nil {
		return e
	}
	guard, e := lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	b, e := agentrelease.ReadPublic(filepath.Join(base, "updater", "private", "transaction.json"), 32768)
	if os.IsNotExist(e) {
		return finishInstall(ctx, base)
	}
	if e != nil {
		return e
	}
	var t transaction
	if e = agentrelease.Decode(b, &t); e != nil {
		return e
	}
	m, e := agentrelease.Verify([]byte(t.Receipt.Manifest), t.Receipt.Signature)
	comparison, comparisonErr := agentrelease.Compare(t.Target, t.Previous)
	if e != nil || t.Target != m.Version || !validTarget(t.Previous) || comparisonErr != nil || comparison <= 0 {
		return errors.New("invalid recovery record")
	}
	if t.Phase == "completed" {
		return finishInstall(ctx, base)
	}
	if e = repairVersion(base, t.Target); e != nil {
		return e
	}
	if !m.Compatible(t.Previous) {
		return errors.New("recovery source is incompatible")
	}
	if t.Phase != "staged" && t.Phase != "switching" && t.Phase != "awaiting_confirmation" && t.Phase != "repair_attempted" {
		return errors.New("invalid recovery phase")
	}
	if t.Phase == "repair_attempted" {
		return errors.New("repair already attempted; inspect the preserved installation")
	}
	if e = verifyDirectory(filepath.Join(base, "releases", t.Target), m); e != nil {
		return e
	}
	c, e := config(base)
	if e != nil {
		return e
	}
	p, e := policy(ctx, base, c, t.Previous)
	if e != nil || p.Candidate == nil || *p.Candidate != t.Receipt {
		return errors.New("recovery target is no longer authorized")
	}
	if e = system(ctx, "is-enabled", "--quiet", Service); e != nil {
		return errors.New("observation service is disabled or masked")
	}
	t.Phase = "repair_attempted"
	if e = record(base, t); e != nil {
		return e
	}
	if e = system(ctx, "stop", Service); e != nil {
		return e
	}
	if e = switchCurrent(base, t.Target); e != nil {
		return e
	}
	if e = atomicFile(filepath.Join(base, "updater", "receipt.json"), t.Receipt, 0644); e != nil {
		return e
	}
	if e = units(ctx, base, filepath.Join(base, "releases", t.Target)); e != nil {
		return e
	}
	if e = system(ctx, "start", Service); e != nil {
		return e
	}
	if e = confirm(ctx, base, c, m); e != nil {
		return e
	}
	t.Phase = "completed"
	if e = record(base, t); e != nil {
		return e
	}
	return cleanup(base, t.Target)
}
func finishInstall(ctx context.Context, base string) error {
	// Resume the existing recoverable enrollment, never issue or create a new identity.
	b, e := agentrelease.ReadPublic(filepath.Join(base, "config.json"), 8192)
	if e != nil {
		return e
	}
	var c agentconfig.Config
	if e = agentrelease.Decode(b, &c); e != nil || !c.Managed || c.StateDir != filepath.Join(base, "data") || c.Updates == nil {
		return errors.New("invalid incomplete installation")
	}
	if c.Updates.HostID == "" {
		if e = command(ctx, "/usr/sbin/runuser", "-u", "tinywarden-agent", "--", filepath.Join(base, "current", "tinywarden-agent"), "resume-enrollment", "--config", filepath.Join(base, "config.json")); e != nil {
			return e
		}
		if e = bind(base, &c); e != nil {
			return e
		}
		if e = saveConfig(base, c); e != nil {
			return e
		}
	}
	dir, m, e := current(base)
	if e != nil {
		return e
	}
	if e = units(ctx, base, dir); e != nil {
		return e
	}
	if e = system(ctx, "enable", "--now", Service); e != nil {
		return e
	}
	if e = confirm(ctx, base, c, m); e != nil {
		return e
	}
	return system(ctx, "enable", "--now", Timer)
}
