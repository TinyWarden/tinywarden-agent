package maintenance

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func TestRecoveryClosedFixtureAndCrossLanguageDigests(t *testing.T) {
	raw, e := os.ReadFile("testdata/maintenance-recovery-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Acceptance      RecoveryAcceptance   `json:"acceptance"`
		AcceptanceSHA   string               `json:"acceptance_sha256"`
		Finalization    RecoveryFinalization `json:"finalization"`
		FinalizationSHA string               `json:"finalization_sha256"`
	}
	if e = controlplane.Decode(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	sha, e := recoveryDigest(fixture.Acceptance)
	if e != nil || sha != fixture.AcceptanceSHA {
		t.Fatal("acceptance digest drift", e)
	}
	sha, e = recoveryDigest(fixture.Finalization)
	if e != nil || sha != fixture.FinalizationSHA {
		t.Fatal("release digest drift", e)
	}
	reply := RecoveryReply{1, fixture.Acceptance.Scope.RecoveryID, fixture.Acceptance.Scope, "2080-01-01T00:00:00.000Z", "accepted", &fixture.AcceptanceSHA, nil}
	record := RecoveryRecord{Schema: 1, State: "accepted", Token: "tw_recovery_v1." + reply.RecoveryID + ".ccccccccccccccccccccccccccccccccccccccccccc", Acceptance: fixture.Acceptance, Accepted: &reply}
	encoded, e := json.Marshal(record)
	if e != nil {
		t.Fatal(e)
	}
	var decoded RecoveryRecord
	if e = controlplane.Decode(encoded, &decoded); e != nil || decoded.Validate() != nil {
		t.Fatal("strict journal reload rejected closed scope", e)
	}
	changed := append([]byte(nil), encoded...)
	changed = append(changed[:len(changed)-1], []byte(`,"unexpected":false}`)...)
	if controlplane.Decode(changed, &decoded) == nil {
		t.Fatal("unknown recovery authority field accepted")
	}
}
