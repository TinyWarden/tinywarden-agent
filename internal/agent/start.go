package agent

import (
	"context"

	loop "github.com/TinyWarden/tinywarden-agent/internal/agent/loop"
	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func Run(ctx context.Context, config agentconfig.Config, client *Client, emit func(string), diagnostics ...loop.Diagnostics) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	store, err := agentstate.OpenStore(config.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return err
	}
	if err := store.UpgradePending(&state); err != nil {
		return err
	}
	if state.Enrollment != nil {
		if err := runCycle(ctx, func(ctx context.Context) error {
			return finishEnrollment(ctx, store, &state, client)
		}, emit); err != nil {
			return err
		}
	}
	if state.Replacement != nil {
		if err := runCycle(ctx, func(ctx context.Context) error {
			return finishReplacement(ctx, store, &state, client)
		}, emit); err != nil {
			return err
		}
	}
	heartbeat := func(ctx context.Context) (loop.HeartbeatOutcome, error) {
		return sendHeartbeatAttempt(ctx, store, &state, client)
	}
	maintenance := newPlatformLane(config, store, &state)
	var diagnostic func(loop.HeartbeatDiagnostic)
	if len(diagnostics) > 0 {
		diagnostic = diagnostics[0].Heartbeat
	}
	monitoring, controlErr := newMonitoringLane(store, client, state, emit)
	if controlErr != nil {
		emit("monitoring_control_invalid")
		return loop.RunHeartbeatLanes(ctx, state.HeartbeatIntervalSeconds, heartbeat, func(context.Context) error { return nil }, emit, diagnostic, maintenance.tick)
	}
	packages, err := newPackageLane(ctx, store, client, state, config, emit)
	if err != nil {
		emit("package_state_unavailable")
		return loop.RunHeartbeatLanes(ctx, state.HeartbeatIntervalSeconds, heartbeat, func(ctx context.Context) error { _, err := client.fetchPackages(ctx, state, false); return err }, emit, diagnostic, maintenance.tick)
	}
	defer packages.stop()
	packages.waitEnabled = true
	if len(diagnostics) > 0 {
		packages.diagnostic = diagnostics[0].Monitoring
	}
	packages.monitoring = monitoring
	monitoring.quiescent = func() bool { return !packages.running }
	if packages.runtime.VerifyAssets() != nil {
		emit("package_runtime_unavailable")
	}
	return loop.RunHeartbeatLanes(ctx, state.HeartbeatIntervalSeconds, heartbeat, func(context.Context) error { return nil }, emit, diagnostic, monitoring.tick, packages.tick, maintenance.tick)
}
