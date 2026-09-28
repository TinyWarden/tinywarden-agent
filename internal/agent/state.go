package agent

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

type Enrollment struct {
	RequestID    string `json:"request_id"`
	Token        string `json:"token"`
	Credential   string `json:"credential"`
	AgentVersion string `json:"agent_version,omitempty"`
	Hostname     string `json:"hostname"`
	OSID         string `json:"os_id"`
	OSVersion    string `json:"os_version"`
	Architecture string `json:"architecture"`
}

type PendingHeartbeat struct {
	Sequence     uint64 `json:"sequence"`
	SentAt       string `json:"sent_at"`
	AgentVersion string `json:"agent_version"`
}

type State struct {
	Version                  int               `json:"version"`
	Enrollment               *Enrollment       `json:"enrollment,omitempty"`
	Replacement              *Enrollment       `json:"replacement,omitempty"`
	Credential               string            `json:"credential,omitempty"`
	HostID                   string            `json:"host_id,omitempty"`
	AgentID                  string            `json:"agent_id,omitempty"`
	Generation               uint64            `json:"generation,omitempty"`
	HeartbeatIntervalSeconds int               `json:"heartbeat_interval_seconds,omitempty"`
	StaleAfterSeconds        int               `json:"stale_after_seconds,omitempty"`
	LastSequence             uint64            `json:"last_sequence,omitempty"`
	PendingHeartbeat         *PendingHeartbeat `json:"pending_heartbeat,omitempty"`
}

type Store struct {
	dir  string
	lock *os.File
}

var agentCredentialPattern = regexp.MustCompile(`^tw_a_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$`)
var agentVersionPattern = regexp.MustCompile(`^[A-Za-z0-9.+-]{1,64}$`)

func validAgentVersion(value string) bool { return agentVersionPattern.MatchString(value) }

func privatePath(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("invalid state path")
	}
	current := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(dir, current), current)
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe state path")
		}
	}
	return nil
}

func OpenStore(dir string) (*Store, error) {
	if err := privatePath(dir); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("unsafe state directory")
	}
	lock, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("state directory already in use")
	}
	return &Store{dir: dir, lock: lock}, nil
}

func (store *Store) Close() error { return store.lock.Close() }

func (store *Store) Load() (State, error) {
	var state State
	path := filepath.Join(store.dir, "state.json")
	info, err := os.Lstat(path)
	if err != nil {
		return state, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		stat.Uid != uint32(os.Geteuid()) || info.Size() > 16384 {
		return state, errors.New("unsafe state file")
	}
	file, err := os.Open(path)
	if err != nil {
		return state, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.Version != 1 {
		return state, errors.New("corrupt state")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return state, errors.New("corrupt state")
	}
	if state.Enrollment == nil && (state.Credential == "" || state.HostID == "" ||
		state.AgentID == "" || state.Generation == 0) {
		return state, errors.New("corrupt state")
	}
	if state.Enrollment != nil {
		pending := state.Enrollment
		if !validID(pending.RequestID) || !tokenPattern.MatchString(pending.Token) ||
			!agentCredentialPattern.MatchString(pending.Credential) ||
			(pending.AgentVersion != "" && !validAgentVersion(pending.AgentVersion)) ||
			pending.Hostname == "" || pending.OSID != "debian" || pending.OSVersion != "13" ||
			(pending.Architecture != "amd64" && pending.Architecture != "arm64") ||
			state.Credential != "" || state.HostID != "" || state.AgentID != "" ||
			state.PendingHeartbeat != nil || state.Replacement != nil {
			return state, errors.New("corrupt state")
		}
	} else if !agentCredentialPattern.MatchString(state.Credential) ||
		!validID(state.HostID) || !validID(state.AgentID) ||
		state.Generation > 9007199254740991 ||
		state.HeartbeatIntervalSeconds < 10 || state.HeartbeatIntervalSeconds > 300 ||
		state.StaleAfterSeconds < 3*state.HeartbeatIntervalSeconds ||
		state.StaleAfterSeconds > 3600 || state.LastSequence > 9007199254740991 {
		return state, errors.New("corrupt state")
	}
	if pending := state.Replacement; pending != nil {
		if !validID(pending.RequestID) || !tokenPattern.MatchString(pending.Token) ||
			!agentCredentialPattern.MatchString(pending.Credential) ||
			(pending.AgentVersion != "" && !validAgentVersion(pending.AgentVersion)) ||
			pending.Hostname == "" || pending.OSID != "debian" || pending.OSVersion != "13" ||
			(pending.Architecture != "amd64" && pending.Architecture != "arm64") ||
			state.Generation >= 9007199254740991 || pending.Credential == state.Credential {
			return state, errors.New("corrupt state")
		}
	}
	if pending := state.PendingHeartbeat; pending != nil {
		if pending.Sequence != state.LastSequence+1 || !validInstant(pending.SentAt) ||
			!validAgentVersion(pending.AgentVersion) {
			return state, errors.New("corrupt state")
		}
	}
	return state, nil
}

// Version-1 pending enrollment records were written only by the 0.0.1 client.
// Freeze their original wire version durably before any replay with a newer binary.
func (store *Store) UpgradePending(state *State) error {
	changed := false
	for _, pending := range []*Enrollment{state.Enrollment, state.Replacement} {
		if pending != nil && pending.AgentVersion == "" {
			pending.AgentVersion = "0.0.1"
			changed = true
		}
	}
	if changed {
		return store.Save(*state)
	}
	return nil
}

func (store *Store) Save(state State) error {
	state.Version = 1
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > 16384 {
		return errors.New("state too large")
	}
	file, err := os.CreateTemp(store.dir, ".state-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(store.dir, "state.json")); err != nil {
		return err
	}
	return store.lock.Sync()
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

func newCredential() (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return "tw_a_" + id + "." + base64.RawURLEncoding.EncodeToString(secret), nil
}
