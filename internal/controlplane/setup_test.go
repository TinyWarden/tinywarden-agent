package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestSetupClosedEnvelope(t *testing.T) {
	token := "tw_e_11111111-1111-4111-8111-111111111111." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	encode := func(payload string) string {
		return SetupPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload))
	}
	b, _ := json.Marshal(Setup{Origin: "https://tenant.example:8443", Token: token})
	if got, e := ParseSetup("\n " + encode(string(b)) + " \n"); e != nil || got.Token != token {
		t.Fatal(e)
	}
	for _, bad := range []string{encode(string(b)) + "=", strings.Replace(encode(string(b)), ".", ".\n", 1), "tw_setup_v2." + encode(string(b)), encode(`{"origin":"https://example","origin":"https://example","token":"` + token + `"}`), encode(`{"origin":"https://example","token":"` + token + `","url":"https://evil"}`), strings.Repeat("a", 4097)} {
		if _, e := ParseSetup(bad); e == nil {
			t.Fatal("invalid setup accepted")
		}
	}
	for _, origin := range []string{"http://example", "https://user@example", "https://example/app", "https://example:65536", "https://example:0", "https://example:", "https://example?foo=bar", "https://example#x"} {
		if _, e := Origin(origin); e == nil {
			t.Fatal(origin)
		}
	}
}
