package legacy

import (
	"encoding/json"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func baselineDigest(scope agentstate.Scope, e baselineEntry) string {
	if e.Assignment == nil {
		return ""
	}
	a := e.Assignment
	r := a.Recipe
	steps := make([]any, len(r.Steps))
	for i, s := range r.Steps {
		steps[i] = []any{s.ID, s.Profile, s.Argv}
	}
	tuple := []any{1, scope.HostID, scope.AgentID, scope.Generation, e.Key, e.ID, e.Revision,
		a.DefinitionRevision, a.PolicyVersion, a.Mode, a.Applicability, r.Capability, a.Normalizer, a.Evaluator,
		a.Interval, a.Timeout, a.StaleAfter, []any{r.SchemaVersion, r.Capability, r.PolicyVersion, r.TimeoutSeconds, steps}}
	data, err := json.Marshal(tuple)
	if err != nil {
		return ""
	}
	return controlplane.BodyDigest(data)
}
