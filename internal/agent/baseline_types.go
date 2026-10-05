package agent

import (
	"encoding/json"
	"reflect"

	"github.com/TinyWarden/tinywarden-agent/internal/runner"
	baseline "github.com/TinyWarden/tinywarden-agent/internal/skills/builtin"
)

const baselineBytes = 48 << 10
const baselineQueueBytes = 1 << 20
const safeCounter = 9007199254740991

var baselineKeys = baseline.Keys()

type baselineScope struct {
	Version    int    `json:"version"`
	Origin     string `json:"origin"`
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
}

func scopeBaseline(origin string, state State) baselineScope {
	return baselineScope{1, origin, state.HostID, state.AgentID, state.Generation}
}
func (s baselineScope) valid() bool {
	return s.Version == 1 && s.Origin != "" && validID(s.HostID) && validID(s.AgentID) && s.Generation >= 1 && s.Generation <= safeCounter
}

type baselineAssignment struct {
	DefinitionRevision uint64        `json:"definition_revision"`
	PolicyVersion      uint64        `json:"policy_version"`
	Mode               string        `json:"mode"`
	Applicability      string        `json:"applicability"`
	Normalizer         string        `json:"normalizer"`
	Evaluator          string        `json:"evaluator"`
	Interval           int           `json:"interval_seconds"`
	Timeout            int           `json:"timeout_seconds"`
	StaleAfter         int           `json:"stale_after_seconds"`
	Recipe             runner.Recipe `json:"recipe"`
}
type baselineEntry struct {
	Key         baseline.Key        `json:"definition_key"`
	ID          string              `json:"assignment_id"`
	Revision    uint64              `json:"revision"`
	Digest      string              `json:"digest"`
	NotModified bool                `json:"not_modified"`
	Assignment  *baselineAssignment `json:"assignment,omitempty"`
}
type baselineResponse struct {
	SchemaVersion int             `json:"schema_version"`
	HostID        string          `json:"host_id"`
	AgentID       string          `json:"agent_id"`
	Generation    uint64          `json:"generation"`
	Poll          int             `json:"poll_interval_seconds"`
	Entries       []baselineEntry `json:"assignments"`
}
type baselineCache struct {
	Scope       baselineScope   `json:"scope"`
	Entries     []baselineEntry `json:"entries"`
	ValidatedAt string          `json:"validated_at"`
	Paused      bool            `json:"paused"`
}

func validBaselineEntry(scope baselineScope, e baselineEntry) bool {
	a := e.Assignment
	if !scope.valid() || a == nil || e.NotModified || !validID(e.ID) || e.Revision < 1 || e.Revision > safeCounter ||
		a.DefinitionRevision < 1 || a.DefinitionRevision > safeCounter || a.PolicyVersion > safeCounter ||
		(a.Mode != "inherit" && a.Mode != "override") || a.Interval < 300 || a.Interval > 86400 || a.Timeout < 1 || a.Timeout > 30 ||
		a.StaleAfter != 3*a.Interval || a.Recipe.TimeoutSeconds != a.Timeout {
		return false
	}
	n, v := baseline.Versions(e.Key)
	if n == "" || a.Normalizer != n || a.Evaluator != v {
		return false
	}
	if a.Applicability != "ready" && a.Applicability != "unsupported_os" && a.Applicability != "unsupported_architecture" && a.Applicability != "missing_capability" && a.Applicability != "disabled" {
		return false
	}
	mode := "upgrade"
	if e.Key == baseline.Packages && len(a.Recipe.Steps) == 1 && len(a.Recipe.Steps[0].Argv) == 3 {
		mode = "with-new-pkgs"
	}
	wanted, err := baseline.Recipe(e.Key, a.Timeout, mode)
	if err != nil || !reflect.DeepEqual(wanted, a.Recipe) {
		return false
	}
	data, err := json.Marshal(a.Recipe)
	if err != nil {
		return false
	}
	if _, err := runner.Parse(data); err != nil {
		return false
	}
	return baselineDigest(scope, e) == e.Digest
}

func validBaselineCache(c baselineCache) bool {
	if !c.Scope.valid() || !validInstant(c.ValidatedAt) || len(c.Entries) != 3 {
		return false
	}
	for i, key := range baselineKeys {
		if c.Entries[i].Key != key || !validBaselineEntry(c.Scope, c.Entries[i]) {
			return false
		}
	}
	return true
}
