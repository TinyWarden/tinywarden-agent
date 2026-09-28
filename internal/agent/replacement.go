package agent

import (
	"context"
	"errors"
)

func StartReplacement(ctx context.Context, config Config, tokenFile string, client *Client) error {
	store, err := OpenStore(config.StateDir)
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
	hostname, osID, osVersion, architecture, err := metadata()
	if err != nil {
		return err
	}
	requestID, err := randomID()
	if err != nil {
		return err
	}
	credential, err := newCredential()
	if err != nil {
		return err
	}
	state.Replacement = &Enrollment{RequestID: requestID, Token: token,
		Credential: credential, Hostname: hostname, OSID: osID,
		OSVersion: osVersion, Architecture: architecture, AgentVersion: Version}
	if err := store.Save(state); err != nil {
		return err
	}
	return finishReplacement(ctx, store, &state, client)
}

func finishReplacement(ctx context.Context, store *Store, state *State, client *Client) error {
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
