package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type EffectiveCheck struct {
	Capability        string `json:"capability"`
	SelectorVersion   int    `json:"selector_version"`
	EvaluatorVersion  int    `json:"evaluator_version"`
	WarningPercent    int    `json:"warning_percent"`
	CriticalPercent   int    `json:"critical_percent"`
	IntervalSeconds   int    `json:"interval_seconds"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	StaleAfterSeconds int    `json:"stale_after_seconds"`
}

type AssignedCheck struct {
	Kind string `json:"kind"`
}
type Assignment struct {
	DefinitionKey      string          `json:"definition_key"`
	DefinitionRevision uint64          `json:"definition_revision"`
	PolicyVersion      uint64          `json:"policy_version"`
	Mode               string          `json:"mode"`
	Applicability      string          `json:"applicability"`
	Effective          EffectiveCheck  `json:"effective"`
	Checks             []AssignedCheck `json:"checks"`
}

type AssignmentCache struct {
	Version     int        `json:"version"`
	Origin      string     `json:"origin"`
	HostID      string     `json:"host_id"`
	AgentID     string     `json:"agent_id"`
	Generation  uint64     `json:"generation"`
	ID          string     `json:"assignment_id"`
	Revision    uint64     `json:"revision"`
	Digest      string     `json:"digest"`
	Assignment  Assignment `json:"assignment"`
	ValidatedAt string     `json:"validated_at,omitempty"`
}

type assignmentResponse struct {
	SchemaVersion       int         `json:"schema_version"`
	HostID              string      `json:"host_id"`
	AgentID             string      `json:"agent_id"`
	Generation          uint64      `json:"generation"`
	ID                  string      `json:"assignment_id"`
	Revision            uint64      `json:"revision"`
	Digest              string      `json:"digest"`
	NotModified         bool        `json:"not_modified"`
	PollIntervalSeconds int         `json:"poll_interval_seconds"`
	Assignment          *Assignment `json:"assignment,omitempty"`
}

func validAssignment(cache AssignmentCache) bool {
	a := cache.Assignment
	e := a.Effective
	if cache.Version != 1 || !validID(cache.HostID) || !validID(cache.AgentID) ||
		!validID(cache.ID) || cache.Generation < 1 || cache.Generation > 9007199254740991 ||
		cache.Revision < 1 || cache.Revision > 9007199254740991 ||
		a.DefinitionKey != "disk-local" || a.DefinitionRevision < 1 ||
		a.DefinitionRevision > 9007199254740991 || a.PolicyVersion > 9007199254740991 ||
		(a.Mode != "inherit" && a.Mode != "override") ||
		e.Capability != "disk_usage.v1" || e.SelectorVersion != 1 || e.EvaluatorVersion != 1 ||
		e.WarningPercent < 1 || e.WarningPercent >= e.CriticalPercent ||
		e.CriticalPercent > 100 || e.IntervalSeconds < 60 || e.IntervalSeconds > 3600 ||
		e.TimeoutSeconds != 10 || e.StaleAfterSeconds != 3*e.IntervalSeconds ||
		len(cache.Digest) != 64 || len(a.Checks) > 1 {
		return false
	}
	if cache.ValidatedAt != "" && !validInstant(cache.ValidatedAt) {
		return false
	}
	if a.Applicability == "ready" {
		if len(a.Checks) != 1 || a.Checks[0].Kind != "disk_usage" {
			return false
		}
	} else if a.Applicability != "unsupported_os" && a.Applicability != "unsupported_architecture" &&
		a.Applicability != "missing_capability" || len(a.Checks) != 0 {
		return false
	}
	bytes, err := hex.DecodeString(cache.Digest)
	if err != nil || hex.EncodeToString(bytes) != cache.Digest {
		return false
	}
	payload, err := json.Marshal([]any{1, cache.HostID, cache.AgentID, cache.Generation,
		"disk-local", cache.Revision, a.DefinitionRevision, a.PolicyVersion, a.Mode,
		a.Applicability, "disk_usage.v1", 1, 1, e.WarningPercent, e.CriticalPercent,
		e.IntervalSeconds, 10, e.StaleAfterSeconds})
	if err != nil {
		return false
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]) == cache.Digest
}

func (cache AssignmentCache) scoped(origin string, state State) bool {
	return cache.Origin == origin && cache.HostID == state.HostID &&
		cache.AgentID == state.AgentID && cache.Generation == state.Generation
}

func (store *Store) LoadAssignment(origin string, state State) (*AssignmentCache, error) {
	path := filepath.Join(store.dir, "assignments.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		stat.Uid != uint32(os.Geteuid()) || info.Size() > 16384 {
		return nil, errors.New("unsafe assignment cache")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16385))
	decoder.DisallowUnknownFields()
	var cache AssignmentCache
	if decoder.Decode(&cache) != nil || !validAssignment(cache) {
		return nil, errors.New("corrupt assignment cache")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("corrupt assignment cache")
	}
	if !cache.scoped(origin, state) {
		return nil, nil
	}
	return &cache, nil
}

func (store *Store) SaveAssignment(cache AssignmentCache) error {
	if !validAssignment(cache) {
		return errors.New("invalid assignment cache")
	}
	data, err := json.Marshal(cache)
	if err != nil || len(data) > 16384 {
		return errors.New("assignment cache too large")
	}
	file, err := os.CreateTemp(store.dir, ".assignments-")
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
	if err := os.Rename(temp, filepath.Join(store.dir, "assignments.json")); err != nil {
		return err
	}
	return store.lock.Sync()
}

// Keep the saved identity for rollback detection, but revoke offline collection after a terminal fault.
func pauseAssignmentLease(store *Store, known **AssignmentCache) error {
	if *known == nil {
		return nil
	}
	paused := **known
	paused.ValidatedAt = ""
	if err := store.SaveAssignment(paused); err != nil {
		*known = nil
		return err
	}
	*known = &paused
	return nil
}

func (client *Client) FetchAssignments(ctx context.Context, state State,
	known *AssignmentCache) (*assignmentResponse, error) {
	var knownWire any
	if known != nil {
		knownWire = map[string]any{"id": known.ID, "revision": known.Revision, "digest": known.Digest}
	}
	wire, err := client.post(ctx, "/api/v1/agent/assignments", state.Credential,
		map[string]any{"schema_version": 1, "agent_version": Version,
			"capabilities": []string{"disk_usage.v1"}, "known_assignment": knownWire}, false)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var response assignmentResponse
	if decoder.Decode(&response) != nil || response.SchemaVersion != 1 ||
		response.HostID != state.HostID || response.AgentID != state.AgentID ||
		response.Generation != state.Generation || !validID(response.ID) ||
		response.Revision < 1 || response.Revision > 9007199254740991 ||
		response.PollIntervalSeconds != 60 {
		return nil, errors.New("invalid assignment response")
	}
	if known != nil && response.Revision < known.Revision {
		return nil, errors.New("assignment revision regressed")
	}
	if response.NotModified {
		if response.Assignment != nil || known == nil || response.ID != known.ID ||
			response.Revision != known.Revision || response.Digest != known.Digest {
			return nil, errors.New("invalid omitted assignment")
		}
	} else {
		if response.Assignment == nil {
			return nil, errors.New("missing assignment")
		}
		candidate := AssignmentCache{Version: 1, Origin: client.Origin, HostID: response.HostID,
			AgentID: response.AgentID, Generation: response.Generation,
			ID: response.ID, Revision: response.Revision, Digest: response.Digest,
			Assignment: *response.Assignment}
		if !validAssignment(candidate) {
			return nil, errors.New("invalid assignment digest")
		}
		if known != nil && response.Revision == known.Revision &&
			(response.ID != known.ID || response.Digest != known.Digest) {
			return nil, errors.New("equal assignment revision mismatch")
		}
	}
	return &response, nil
}

func receiveAssignment(store *Store, origin string, state State, known **AssignmentCache,
	response *assignmentResponse) error {
	if response.NotModified {
		if *known == nil || !(*known).scoped(origin, state) ||
			response.ID != (*known).ID || response.Revision != (*known).Revision ||
			response.Digest != (*known).Digest {
			return errors.New("stale omitted assignment")
		}
		updated := **known
		updated.ValidatedAt = time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
		if err := store.SaveAssignment(updated); err != nil {
			return err
		}
		*known = &updated
		return nil
	}
	if response.Assignment == nil {
		return errors.New("missing assignment")
	}
	cache := AssignmentCache{Version: 1, Origin: origin, HostID: response.HostID,
		AgentID: response.AgentID, Generation: response.Generation,
		ID: response.ID, Revision: response.Revision, Digest: response.Digest,
		Assignment: *response.Assignment}
	cache.ValidatedAt = time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
	if !cache.scoped(origin, state) || !validAssignment(cache) {
		return errors.New("invalid assignment")
	}
	if *known != nil && (cache.Revision < (*known).Revision ||
		(cache.Revision == (*known).Revision &&
			(cache.ID != (*known).ID || cache.Digest != (*known).Digest))) {
		return fmt.Errorf("assignment response reordered")
	}
	if err := store.SaveAssignment(cache); err != nil {
		return err
	}
	*known = &cache
	return nil
}
