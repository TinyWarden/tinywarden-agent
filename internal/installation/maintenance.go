package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
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
	client := controlplane.NewClient(c.ControlPlaneOrigin)
	registry := maintenanceRegistry(base, c.RuntimeAssets, guard, verify, client, s.Credential)
	return maintenance.Coordinator{Base: base, Scope: scope, Registry: registry, Schema: 2, Call: func(ctx context.Context, request maintenance.Request) (maintenance.Reply, error) {
		if request.Kind == "poll" && request.JobID == nil {
			status, _ := readMaintenanceStatus(base)
			request.Data, _ = json.Marshal(struct {
				Cursor      *string                           `json:"probe_cursor"`
				WorkerReady bool                              `json:"worker_ready"`
				Origins     []actioncontract.OriginPermission `json:"origins"`
				Readiness   []actioncontract.Readiness        `json:"readiness"`
			}{status.ProbeCursor, status.Report.WorkerReady, status.Origins, status.ActionReadiness})
		}
		reply, err := maintenance.Post(ctx, client, s.Credential, request, registry)
		if err == nil && request.JobID == nil {
			if e := maintenanceStatus(ctx, base, m, maintenanceProbeUpdate{reply.Probes, reply.NextProbeCursor}); e != nil {
				return maintenance.Reply{}, e
			}
		}
		return reply, err
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
	// Drain a bounded set of readiness pages while idle. Cursor progress is durable
	// across polls; no action is invoked by these declarative prerequisite probes.
	for page := 0; e == nil && j == nil && page < 7; page++ {
		before, err := readMaintenanceStatus(base)
		if err != nil || before.ProbeCursor == nil {
			break
		}
		j, e = c.Discover(ctx)
		after, err := readMaintenanceStatus(base)
		if err != nil || after.ProbeCursor == nil || *after.ProbeCursor == *before.ProbeCursor {
			break
		}
	}
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
	unit := "/tinywarden-agent-maintenance-worker@" + job + ".service"
	if e != nil || len(raw) > 4096 || !(strings.Contains(string(raw), unit+"\n") || strings.Contains(string(raw), unit+"/supervisor\n")) {
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
