package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArtifactQuotaPreservesControlLimitAndExactJSON(t *testing.T) {
	row := `{"name":"example","architecture":"amd64","installed_version":"1","candidate_version":"2","archive_sha256":"` + strings.Repeat("a", 64) + `","archive_bytes":123,"installed_bytes":456}`
	raw := []byte(`{"plan":{"packages":[` + strings.Repeat(row+",", 4999) + row + `]}}`)
	var value struct {
		Plan json.RawMessage `json:"plan"`
	}
	if Decode(raw, &value) == nil {
		t.Fatal("large artifact silently relaxed ordinary control quota")
	}
	if err := DecodeArtifact(raw, &value, 180000); err != nil {
		t.Fatal("5000-row byte-bounded artifact rejected", err)
	}
	for _, invalid := range []string{
		`{"plan":{"name":"first","name":"second"}}`,
		`{"plan":{},"unexpected":true}`,
		`{"plan":` + strings.Repeat("[", 17) + `0` + strings.Repeat("]", 17) + `}`,
	} {
		if DecodeArtifact([]byte(invalid), &value, 180000) == nil {
			t.Fatal("large decoder accepted ambiguous JSON", invalid)
		}
	}
	if DecodeArtifact(raw, &value, 32) == nil || DecodeArtifact(raw, &value, 200001) == nil {
		t.Fatal("artifact quota not bounded")
	}
}
