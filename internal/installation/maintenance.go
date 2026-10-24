package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	aptmaintenance "github.com/TinyWarden/tinywarden-agent/internal/maintenance/apt"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

func maintenanceCurrent(base string) (agentrelease.Manifest, error) {
	_, m, e := current(base)
	if e != nil {
		return m, e
	}
	hash, e := runningDigest()
	if e != nil || hash != m.BinarySHA256 {
		return m, errors.New("maintenance_running_identity_unverified")
	}
	return m, nil
}
func runningDigest() (string, error) {
	// Fixed kernel magic-link: hash the executing inode, including a replaced file.
	f, e := os.Open("/proc/self/exe")
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return "", errors.New("maintenance_running_identity_unverified")
	}
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(f, (64<<20)+1))
	if e != nil || n > 64<<20 {
		return "", errors.New("maintenance_running_identity_unverified")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func maintenanceStatus(ctx context.Context, base string, m agentrelease.Manifest) error {
	report := agentrelease.MaintenanceReadiness{Capability: maintenance.Capability, WorkerReady: true, Actions: []agentrelease.MaintenanceActionReadiness{}}
	for _, name := range maintenanceUnits {
		if ownedRegistration(base, name) != nil {
			report.WorkerReady = false
			continue
		}
		template, e := os.ReadFile(filepath.Join(base, "current", "systemd", name))
		if e != nil {
			report.WorkerReady = false
			continue
		}
		installed, e := agentrelease.ReadPublic(filepath.Join(base, "updater", "systemd", name), 16384)
		if e != nil || !bytes.Equal(installed, []byte(strings.ReplaceAll(string(template), "@BASE@", unitQuote(base)))) {
			report.WorkerReady = false
		}
	}
	if system(ctx, "is-enabled", "--quiet", MaintenanceTimer) != nil || system(ctx, "is-active", "--quiet", MaintenanceTimer) != nil {
		report.WorkerReady = false
	}
	if pending, e := pendingTransaction(base); e != nil || pending != nil {
		report.WorkerReady = false
	}
	missing := aptmaintenance.Prerequisites()
	for _, id := range []string{"packages.refresh.v1", "packages.upgrade_conservative.v1"} {
		report.Actions = append(report.Actions, agentrelease.MaintenanceActionReadiness{ID: id, Ready: len(missing) == 0, Missing: missing})
	}
	return atomicFile(filepath.Join(base, "updater", "maintenance.json"), agentrelease.MaintenanceStatus{Schema: 1, BinarySHA: m.BinarySHA256, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Report: report}, 0644)
}
func maintenanceCoordinator(base string, guard *os.File) (maintenance.Coordinator, error) {
	c, e := config(base)
	if e != nil {
		return maintenance.Coordinator{}, e
	}
	s, e := identity(base, c)
	if e != nil {
		return maintenance.Coordinator{}, e
	}
	m, e := maintenanceCurrent(base)
	if e != nil {
		return maintenance.Coordinator{}, e
	}
	if pending, e := pendingTransaction(base); e != nil || pending != nil {
		return maintenance.Coordinator{}, errors.New("maintenance_update_unsettled")
	}
	scope := maintenance.Scope{HostID: s.HostID, AgentID: s.AgentID, Generation: s.Generation}
	verify := func() error {
		now, e := maintenanceCurrent(base)
		if e != nil || now.BinarySHA256 != m.BinarySHA256 {
			return errors.New("maintenance_running_identity_unverified")
		}
		cfg, e := config(base)
		if e != nil {
			return e
		}
		id, e := identity(base, cfg)
		if e != nil || id.HostID != scope.HostID || id.AgentID != scope.AgentID || id.Generation != scope.Generation {
			return errors.New("maintenance_scope_changed")
		}
		return nil
	}
	registry := aptmaintenance.Registry(base, guard, verify)
	client := controlplane.NewClient(c.ControlPlaneOrigin)
	return maintenance.Coordinator{Base: base, Scope: scope, Registry: registry, Call: func(ctx context.Context, request maintenance.Request) (maintenance.Reply, error) {
		return maintenance.Post(ctx, client, s.Credential, request, registry)
	}}, nil
}

func PollMaintenance(ctx context.Context, base string) error {
	if e := Preflight(base, false); e != nil {
		return e
	}
	m, e := maintenanceCurrent(base)
	if e != nil {
		return e
	}
	// This root-owned readiness probe remains fresh during a long native worker.
	// It grants no execution authority and never modifies package state.
	if e = maintenanceStatus(ctx, base, m); e != nil {
		return e
	}
	guard, e := hostmutation.Lock(base)
	if errors.Is(e, hostmutation.ErrBusy) {
		return nil
	}
	if e != nil {
		return e
	}
	if active, err := activeMaintenanceWorkers(""); err != nil || active {
		guard.Close()
		return err
	}
	if e = bootstrapMaintenance(ctx, base); e != nil {
		guard.Close()
		return e
	}
	recovery, e := maintenanceRecoveryCoordinator(base, guard)
	if e == nil {
		e = recovery.RetryRecoveryDelivery(ctx)
	}
	if e != nil {
		guard.Close()
		return e
	}
	if pending, err := maintenance.PendingRecovery(base); err != nil || pending {
		guard.Close()
		return err
	}
	c, e := maintenanceCoordinator(base, guard)
	if e != nil {
		guard.Close()
		return e
	}
	j, e := c.Discover(ctx)
	guard.Close()
	if e != nil || j == nil {
		return e
	}
	return system(ctx, "start", "--no-block", "tinywarden-agent-maintenance-worker@"+j.Intent.ID+".service")
}
func WorkMaintenance(ctx context.Context, base, job string) error {
	if !agentrelease.UUIDPattern.MatchString(job) {
		return errors.New("maintenance_scope_invalid")
	}
	if e := Preflight(base, false); e != nil {
		return e
	}
	raw, e := os.ReadFile("/proc/self/cgroup")
	if e != nil || len(raw) > 4096 || !strings.Contains(string(raw), "/tinywarden-agent-maintenance-worker@"+job+".service\n") {
		return errors.New("maintenance_worker_unit_required")
	}
	guard, e := hostmutation.Lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	if e := assertNoRecoveryDelivery(base); e != nil {
		return e
	}
	if active, err := activeMaintenanceWorkers("tinywarden-agent-maintenance-worker@" + job + ".service"); err != nil || active {
		if err != nil {
			return err
		}
		return hostmutation.ErrBusy
	}
	j, e := maintenance.Load(base)
	if e != nil || j.Intent.ID != job {
		return errors.New("maintenance_scope_invalid")
	}
	fence, e := hostmutation.ReadFence(base)
	if e != nil || fence == nil || *fence != j.Intent.Fence() {
		return errors.New("maintenance_reconciliation_required")
	}
	c, e := maintenanceCoordinator(base, guard)
	if e != nil {
		return e
	}
	return c.Work(ctx, j)
}
