package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func baselineDigest(scope baselineScope, e baselineEntry) string {
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
	return baselineBodyDigest(data)
}
func baselineBodyDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
