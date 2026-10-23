package agent

import (
	"context"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

// ResumeEnrollment repeats only the existing persisted handshake. It never creates credentials.

func ResumeEnrollment(ctx context.Context, config agentconfig.Config, client *Client) error {
	store, err := agentstate.OpenStore(config.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return err
	}
	if state.Enrollment == nil {
		return nil
	}
	return finishEnrollment(ctx, store, &state, client)
}
