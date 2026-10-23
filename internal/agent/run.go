package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	loop "github.com/TinyWarden/tinywarden-agent/internal/agent/loop"
	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	host "github.com/TinyWarden/tinywarden-agent/internal/host"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func readToken(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		stat.Uid != uint32(os.Geteuid()) || info.Size() > 200 {
		return "", errors.New("unsafe token file")
	}
	bytes, err := io.ReadAll(io.LimitReader(file, 201))
	if err != nil {
		return "", err
	}
	if len(bytes) > 200 {
		return "", errors.New("unsafe token file")
	}
	token := strings.TrimSuffix(string(bytes), "\n")
	if !agentstate.TokenPattern.MatchString(token) {
		return "", errors.New("invalid enrollment token")
	}
	return token, nil
}

func StartEnrollment(ctx context.Context, config agentconfig.Config, tokenFile string, client *Client) error {
	store, err := agentstate.OpenStore(config.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	_, err = store.Load()
	if err == nil {
		return errors.New("state already exists")
	}
	if !os.IsNotExist(err) {
		return err
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
	state := agentstate.State{Version: 1, Enrollment: &agentstate.Enrollment{RequestID: requestID,
		Token: token, Credential: credential, Hostname: hostname, OSID: osID,
		OSVersion: osVersion, Architecture: architecture, AgentVersion: Version}}
	if err := store.Save(state); err != nil {
		return err
	}
	return finishEnrollment(ctx, store, &state, client)
}

func finishEnrollment(ctx context.Context, store *agentstate.Store, state *agentstate.State, client *Client) error {
	if state.Enrollment == nil {
		return errors.New("no pending enrollment")
	}
	result, err := client.Enroll(ctx, *state.Enrollment)
	if err != nil {
		return err
	}
	state.Credential = state.Enrollment.Credential
	state.HostID = result.HostID
	state.AgentID = result.AgentID
	state.Generation = result.Generation
	state.HeartbeatIntervalSeconds = result.Interval
	state.StaleAfterSeconds = result.StaleAfter
	state.Enrollment = nil
	return store.Save(*state)
}

func sendHeartbeat(ctx context.Context, store *agentstate.Store, state *agentstate.State, client *Client) error {
	_, err := sendHeartbeatAttempt(ctx, store, state, client)
	return err
}

func sendHeartbeatAttempt(ctx context.Context, store *agentstate.Store, state *agentstate.State, client *Client) (loop.HeartbeatOutcome, error) {
	if state.PendingHeartbeat == nil {
		if state.LastSequence >= 9007199254740991 {
			return loop.HeartbeatOutcome{}, loop.HeartbeatError("pending_state", errors.New("sequence exhausted"))
		}
		state.PendingHeartbeat = &agentstate.PendingHeartbeat{Sequence: state.LastSequence + 1,
			SentAt:       time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z"),
			AgentVersion: Version}
		if err := store.Save(*state); err != nil {
			return loop.HeartbeatOutcome{}, loop.HeartbeatError("pending_state", err)
		}
	}
	result, err := client.Heartbeat(ctx, state.Credential, *state.PendingHeartbeat)
	if err != nil {
		return loop.HeartbeatOutcome{}, loop.HeartbeatError("exchange", err)
	}
	if result.Interval != state.HeartbeatIntervalSeconds || result.StaleAfter != state.StaleAfterSeconds {
		return loop.HeartbeatOutcome{}, loop.HeartbeatError("cadence", errors.New("cadence changed unexpectedly"))
	}
	state.LastSequence = result.Sequence
	state.PendingHeartbeat = nil
	if err := store.Save(*state); err != nil {
		return loop.HeartbeatOutcome{}, loop.HeartbeatError("ack_state", err)
	}
	return loop.HeartbeatOutcome{Duplicate: result.Duplicate}, nil
}

func runCycle(ctx context.Context, attempt func(context.Context) error, emit func(string)) error {
	delays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	var lastError error
	for index := 0; ; index++ {
		err := attempt(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if controlplane.Terminal(err) {
			return err
		}
		if index < len(delays) {
			if err := loop.Wait(ctx, controlplane.RetryWait(err, delays[index])); err != nil {
				return err
			}
			continue
		}
		lastError = err
		break
	}
	emit("degraded")
	for {
		if err := loop.Wait(ctx, controlplane.DegradedWait(lastError)); err != nil {
			return err
		}
		err := attempt(ctx)
		if err == nil {
			emit("recovered")
			return nil
		}
		if controlplane.Terminal(err) {
			return err
		}
		lastError = err
	}
}
