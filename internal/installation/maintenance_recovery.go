package installation

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	aptmaintenance "github.com/TinyWarden/tinywarden-agent/internal/maintenance/apt"
)

func maintenanceRecoveryCoordinator(base string, guard *os.File) (maintenance.RecoveryCoordinator, error) {
	cfg, e := config(base)
	if e != nil {
		return maintenance.RecoveryCoordinator{}, e
	}
	id, e := identity(base, cfg)
	if e != nil {
		return maintenance.RecoveryCoordinator{}, e
	}
	manifest, e := maintenanceCurrent(base)
	if e != nil {
		return maintenance.RecoveryCoordinator{}, e
	}
	verify := func() error {
		current, e := maintenanceCurrent(base)
		if e != nil || current.BinarySHA256 != manifest.BinarySHA256 {
			return errors.New("maintenance_running_identity_unverified")
		}
		now, e := config(base)
		if e != nil || now.ControlPlaneOrigin != cfg.ControlPlaneOrigin {
			return errors.New("maintenance_scope_changed")
		}
		state, e := identity(base, now)
		if e != nil || state.HostID != id.HostID || state.AgentID != id.AgentID || state.Generation != id.Generation {
			return errors.New("maintenance_scope_changed")
		}
		pending, e := pendingTransaction(base)
		if e != nil || pending != nil {
			return errors.New("maintenance_update_unsettled")
		}
		active, e := activeMaintenanceWorkers("")
		if e != nil {
			return e
		}
		if active {
			return hostmutation.ErrBusy
		}
		return nil
	}
	return maintenance.RecoveryCoordinator{Base: base, Registry: aptmaintenance.Registry(base, guard, verify), Call: maintenance.RecoveryPost(controlplane.NewClient(cfg.ControlPlaneOrigin)),
		Verify: func(scope maintenance.RecoveryScope) error {
			if scope.HostID != id.HostID || scope.AgentID != id.AgentID || scope.Generation != id.Generation {
				return errors.New("maintenance_scope_changed")
			}
			return verify()
		}}, nil
}
func readRecoveryAuthorization(input io.Reader, text MaintenanceMessages) (string, error) {
	var token string
	if input == nil {
		value, e := readHidden(text.RecoveryPrompt, text.RecoveryInput, text.RecoveryInput)
		if e != nil {
			return "", e
		}
		token = strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
	} else {
		raw, e := io.ReadAll(io.LimitReader(input, 256))
		if e != nil || len(raw) >= 256 {
			return "", errors.New(text.RecoveryInput)
		}
		token = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	}
	if !maintenance.RecoveryTokenValid(token) {
		return "", errors.New(text.RecoveryInput)
	}
	return token, nil
}
func ReconcileMaintenance(ctx context.Context, base string, input io.Reader, prompt bool, text MaintenanceMessages) error {
	if e := Preflight(base, false); e != nil {
		return e
	}
	guard, e := hostmutation.Lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	coordinator, e := maintenanceRecoveryCoordinator(base, guard)
	if e != nil {
		return e
	}
	saved, e := maintenance.LoadRecovery(base)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	token := ""
	if input != nil || prompt || saved == nil || saved.State == "finalized" {
		token, e = readRecoveryAuthorization(input, text)
		if e != nil {
			return e
		}
	}
	return coordinator.Reconcile(ctx, token)
}
func assertNoRecoveryDelivery(base string) error {
	pending, e := maintenance.PendingRecovery(base)
	if e != nil {
		return e
	}
	if pending {
		return errors.New("maintenance_recovery_delivery_pending")
	}
	return nil
}
