package agent

import (
	"context"
	"errors"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	host "github.com/TinyWarden/tinywarden-agent/internal/host"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func StartReplacement(ctx context.Context, config agentconfig.Config, tokenFile string, client *Client) error {
	store, err := agentstate.OpenStore(config.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.Load()
	if err != nil {
		return err
	}
	if state.Enrollment != nil || state.Replacement != nil ||
		state.Generation >= 9007199254740991 {
		return errors.New("replacement unavailable")
	}
	token, err := readToken(tokenFile)
	if err != nil {
		return err
	}
	hostname, osID, osVersion, architecture, err := host.Metadata()
	if err != nil {
		return err
	}
	requestID, err := agentstate.RandomID()
	if err != nil {
		return err
	}
	credential, err := agentstate.NewCredential()
	if err != nil {
		return err
	}
	state.Replacement = &agentstate.Enrollment{RequestID: requestID, Token: token,
		Credential: credential, Hostname: hostname, OSID: osID,
		OSVersion: osVersion, Architecture: architecture, AgentVersion: Version}
	if err := store.Save(state); err != nil {
		return err
	}
	return finishReplacement(ctx, store, &state, client)
}

func finishReplacement(ctx context.Context, store *agentstate.Store, state *agentstate.State, client *Client) error {
	if state.Replacement == nil {
		return errors.New("no pending replacement")
	}
	result, err := client.Enroll(ctx, *state.Replacement)
	if err != nil {
		return err
	}
	if result.HostID != state.HostID || result.AgentID != state.AgentID ||
		result.Generation != state.Generation+1 ||
		result.Interval != state.HeartbeatIntervalSeconds ||
		result.StaleAfter != state.StaleAfterSeconds {
		return errors.New("invalid replacement response")
	}
	state.Credential = state.Replacement.Credential
	state.Generation = result.Generation
	state.LastSequence = 0
	state.PendingHeartbeat = nil
	state.Replacement = nil
	return store.Save(*state)
}
