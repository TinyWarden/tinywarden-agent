package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

// JSON bytes remain opaque inside the acceptance digest. Numbers and Unicode
// cannot change identity through another language's JSON reserialization.
type ActionAuditFacts struct {
	JSON   string `json:"json"`
	SHA256 string `json:"sha256"`
}

func NewActionAuditFacts(raw json.RawMessage) (ActionAuditFacts, error) {
	sum := sha256.Sum256(raw)
	value := ActionAuditFacts{JSON: string(raw), SHA256: hex.EncodeToString(sum[:])}
	return value, validateActionAudit(value)
}

func validateActionAudit(value any) error {
	bad := errors.New("maintenance_recovery_invalid")
	raw, err := json.Marshal(value)
	var facts ActionAuditFacts
	if err != nil || len(raw) > 2*MaxControl+1024 || controlplane.Decode(raw, &facts) != nil || len(facts.JSON) > MaxControl || !digestPattern.MatchString(facts.SHA256) {
		return bad
	}
	sum := sha256.Sum256([]byte(facts.JSON))
	if hex.EncodeToString(sum[:]) != facts.SHA256 {
		return bad
	}
	var object map[string]json.RawMessage
	if controlplane.DecodeArtifact([]byte(facts.JSON), &object, 8192) != nil || object == nil || len(object) > 64 {
		return bad
	}
	return nil
}
