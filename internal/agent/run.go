package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var tokenPattern = regexp.MustCompile(`^tw_e_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$`)

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
	if !tokenPattern.MatchString(token) {
		return "", errors.New("invalid enrollment token")
	}
	return token, nil
}

func metadata() (string, string, string, string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", "", "", "", err
	}
	if len(hostname) < 1 || len(hostname) > 253 {
		return "", "", "", "", errors.New("invalid hostname")
	}
	for _, char := range hostname {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '.' && char != '-' {
			return "", "", "", "", errors.New("invalid hostname")
		}
	}
	contents, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", "", "", "", err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(contents), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found && (key == "ID" || key == "VERSION_ID") {
			values[key] = strings.Trim(value, `"'`)
		}
	}
	if values["ID"] != "debian" || values["VERSION_ID"] != "13" {
		return "", "", "", "", errors.New("unsupported distribution")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", "", "", "", errors.New("unsupported architecture")
	}
	return hostname, values["ID"], values["VERSION_ID"], runtime.GOARCH, nil
}

func StartEnrollment(ctx context.Context, config Config, tokenFile string, client *Client) error {
	store, err := OpenStore(config.StateDir)
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
	state := State{Version: 1, Enrollment: &Enrollment{RequestID: requestID,
		Token: token, Credential: credential, Hostname: hostname, OSID: osID,
		OSVersion: osVersion, Architecture: architecture, AgentVersion: Version}}
	if err := store.Save(state); err != nil {
		return err
	}
	return finishEnrollment(ctx, store, &state, client)
}

func finishEnrollment(ctx context.Context, store *Store, state *State, client *Client) error {
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

func sendHeartbeat(ctx context.Context, store *Store, state *State, client *Client) error {
	if state.PendingHeartbeat == nil {
		if state.LastSequence >= 9007199254740991 {
			return errors.New("sequence exhausted")
		}
		state.PendingHeartbeat = &PendingHeartbeat{Sequence: state.LastSequence + 1,
			SentAt:       time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z"),
			AgentVersion: Version}
		if err := store.Save(*state); err != nil {
			return err
		}
	}
	result, err := client.Heartbeat(ctx, state.Credential, *state.PendingHeartbeat)
	if err != nil {
		return err
	}
	if result.Interval != state.HeartbeatIntervalSeconds || result.StaleAfter != state.StaleAfterSeconds {
		return errors.New("cadence changed unexpectedly")
	}
	state.LastSequence = result.Sequence
	state.PendingHeartbeat = nil
	return store.Save(*state)
}

func terminal(err error) bool {
	var response *ResponseError
	if errors.As(err, &response) {
		return response.Status != 429 && response.Status < 500
	}
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	if errors.As(err, &authority) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &verification) {
		return true
	}
	var readError *responseReadError
	if errors.As(err, &readError) {
		return false
	}
	var netError net.Error
	return !errors.As(err, &netError)
}

func retryWait(err error, base time.Duration) time.Duration {
	delay := base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
	var response *ResponseError
	if errors.As(err, &response) && response.RetryAfter > delay {
		return response.RetryAfter
	}
	return delay
}

func degradedWait(err error) time.Duration {
	delay := 300*time.Second + time.Duration(rand.Int64N(int64(30*time.Second)+1))
	var response *ResponseError
	if errors.As(err, &response) && response.RetryAfter > delay {
		return response.RetryAfter
	}
	return delay
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
		if terminal(err) {
			return err
		}
		if index < len(delays) {
			if err := wait(ctx, retryWait(err, delays[index])); err != nil {
				return err
			}
			continue
		}
		lastError = err
		break
	}
	emit("degraded")
	for {
		if err := wait(ctx, degradedWait(lastError)); err != nil {
			return err
		}
		err := attempt(ctx)
		if err == nil {
			emit("recovered")
			return nil
		}
		if terminal(err) {
			return err
		}
		lastError = err
	}
}

func Run(ctx context.Context, config Config, client *Client, emit func(string)) error {
	store, err := OpenStore(config.StateDir)
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
	for {
		if err := runCycle(ctx, func(ctx context.Context) error {
			return sendHeartbeat(ctx, store, &state, client)
		}, emit); err != nil {
			return err
		}
		jitter := time.Duration(rand.Int64N(int64(state.HeartbeatIntervalSeconds)*100_000_000 + 1))
		if err := wait(ctx, time.Duration(state.HeartbeatIntervalSeconds)*time.Second+jitter); err != nil {
			return err
		}
	}
}
