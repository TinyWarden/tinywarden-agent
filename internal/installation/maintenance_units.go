package installation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const MaintenanceService = "tinywarden-agent-maintenance.service"
const MaintenanceTimer = "tinywarden-agent-maintenance.timer"
const MaintenanceWorker = "tinywarden-agent-maintenance-worker@.service"

var maintenanceUnits = []string{MaintenanceService, MaintenanceTimer, MaintenanceWorker}

// Optional as one complete set: older signed releases have three templates and
// remain valid rollback targets. A partial new set is never activated.
func templateSet(release string) ([]string, bool, error) {
	names := []string{Service, UpdateService, Timer}
	found := 0
	for _, name := range maintenanceUnits {
		if _, e := os.Lstat(filepath.Join(release, "systemd", name)); e == nil {
			found++
		} else if !os.IsNotExist(e) {
			return nil, false, e
		}
	}
	if found != 0 && found != len(maintenanceUnits) {
		return nil, false, errors.New("incomplete maintenance service templates")
	}
	if found > 0 {
		names = append(names, maintenanceUnits...)
	}
	return names, found > 0, nil
}
func ownedRegistration(base, name string) error {
	path := filepath.Join("/etc/systemd/system", name)
	target, e := os.Readlink(path)
	if e != nil || target != filepath.Join(base, "updater", "systemd", name) {
		return errors.New("unrecognized service registration: " + path)
	}
	return nil
}
func removeMaintenanceUnits(ctx context.Context, base string) error {
	present := []string{}
	for _, name := range maintenanceUnits {
		if _, e := os.Lstat(filepath.Join("/etc/systemd/system", name)); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		if e := ownedRegistration(base, name); e != nil {
			return e
		}
		present = append(present, name)
	}
	if len(present) == 0 {
		return nil
	}
	if e := stopMaintenancePolling(ctx, present); e != nil {
		return e
	}
	// No active package worker is ever stopped. The common root fence must be idle.
	for _, name := range present {
		if e := os.Remove(filepath.Join("/etc/systemd/system", name)); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if e := os.Remove(filepath.Join(base, "updater", "private", "maintenance-bootstrap.json")); e != nil && !os.IsNotExist(e) {
		return e
	}
	return nil
}
func stopMaintenancePolling(ctx context.Context, present []string) error {
	registeredTimer := false
	for _, name := range present {
		if name == MaintenanceTimer {
			registeredTimer = true
		}
	}
	if registeredTimer {
		if e := stopAndDisableUnit(ctx, MaintenanceTimer); e != nil {
			return fmt.Errorf("maintenance timer disable failed: %w", e)
		}
	} else if !maintenanceUnitInactive(ctx, MaintenanceTimer) {
		return errors.New("maintenance timer registration missing while its state is not idle")
	}
	if e := stopIdleManagedUnit(ctx, MaintenanceService); e != nil {
		return fmt.Errorf("maintenance poller stop failed: %w", e)
	}
	return nil
}

// Stop before disable: systemd removes externally linked primary registrations
// during disable, so --now can otherwise fail to stop an already unloaded unit.
func stopAndDisableUnit(ctx context.Context, name string) error {
	if e := stopIdleManagedUnit(ctx, name); e != nil {
		return e
	}
	return system(ctx, "disable", name)
}
func stopIdleManagedUnit(ctx context.Context, name string) error {
	if e := system(ctx, "stop", name); e != nil && !maintenanceUnitInactive(ctx, name) {
		return e
	}
	return nil
}
func maintenanceUnitInactive(ctx context.Context, name string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "is-active", name)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	out, e := cmd.Output()
	if ctx.Err() != nil || len(out) > 128 {
		return false
	}
	if e != nil {
		exit, ok := e.(*exec.ExitError)
		if !ok || (exit.ExitCode() != 3 && exit.ExitCode() != 4) {
			return false
		}
	}
	state := strings.TrimSpace(string(out))
	return state == "inactive" || state == "failed" || state == "unknown"
}

// Signed0.0.18 installs only its known three templates. The next new root updater
// poll bootstraps the optional set even when automatic updates are disabled.
func bootstrapMaintenance(ctx context.Context, base string) error {
	release, _, e := current(base)
	if e != nil {
		return e
	}
	return bootstrapVerifiedMaintenance(ctx, base, release)
}

// Caller has verified the signed current release and holds the root guard.
func bootstrapVerifiedMaintenance(ctx context.Context, base, release string) error {
	_, supported, e := templateSet(release)
	if e != nil || !supported {
		return e
	}
	if _, e = os.Lstat(filepath.Join(base, "updater", "private", "maintenance-bootstrap.json")); e == nil {
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = verifyUnits(ctx, base, release); e != nil {
		return e
	}
	return units(ctx, base, release)
}
func enableMaintenance(ctx context.Context, release string) error {
	_, supported, e := templateSet(release)
	if e != nil || !supported {
		return e
	}
	return system(ctx, "enable", "--now", MaintenanceTimer)
}
