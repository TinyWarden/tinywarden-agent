package actioncontract

import "testing"

func TestCanonicalKeepsOpaqueDomainBytesAndASCIIProtocolIdentity(t *testing.T) {
	raw, err := Canonical(map[string]any{"z": 1, "input_json": `{"value":1.0,"name":"é😀<>&"}`})
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"input_json":"{\"value\":1.0,\"name\":\"\u00e9\ud83d\ude00<>&\"}","z":1}`
	if string(raw) != expected {
		t.Fatalf("canonical mismatch\n%s\n%s", raw, expected)
	}
	if _, err = Canonical(map[string]any{"unsafe_domain_number": 1.5}); err == nil {
		t.Fatal("domain number entered protocol hash")
	}
}
