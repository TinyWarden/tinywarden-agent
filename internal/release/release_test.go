package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture() Manifest {
	return Manifest{Format: 1, Repository: Repository, Tag: "v0.0.9", Version: "0.0.9", SourceCommit: strings.Repeat("a", 40), Channel: "stable", OS: "debian", OSVersion: "13", Architecture: "amd64", SourceMin: "0.0.8", SourceMax: "0.0.9", Automatic: true, Capabilities: []string{Capability}, BinarySHA256: strings.Repeat("1", 64), RuntimeSHA256: strings.Repeat("2", 64), DescriptorSHA256: strings.Repeat("3", 64), Asset: Asset{Name: "tinywarden-agent-0.0.9-linux-amd64.tar.gz", Size: 1024, SHA256: strings.Repeat("4", 64)}}
}
func TestSignedReleaseBoundary(t *testing.T) {
	key, private, _ := ed25519.GenerateKey(rand.Reader)
	m := fixture()
	b, _ := json.Marshal(m)
	sign := func(b []byte) string { return base64.StdEncoding.EncodeToString(ed25519.Sign(private, b)) }
	if got, e := verify(b, sign(b), key); e != nil || !got.Compatible("0.0.8") {
		t.Fatal(e)
	}
	if _, e := Verify(b, sign(b)); e == nil {
		t.Fatal("test signer became production trust")
	}
	for _, bad := range [][]byte{append(append([]byte{}, b...), ' '), []byte(strings.Replace(string(b), `"version":"0.0.9"`, `"version":"0.0.8"`, 1))} {
		if _, e := verify(bad, sign(b), key); e == nil {
			t.Fatal("tampered exact bytes accepted")
		}
	}
	for _, bad := range []string{strings.Replace(string(b), `"format":1`, `"format":1,"format":1`, 1), strings.Replace(string(b), `"format":1`, `"unexpected":true,"format":1`, 1), strings.Replace(string(b), `"version":"0.0.9"`, `"version":"00.0.9"`, 1), strings.Replace(string(b), `"size":1024`, `"size":67108865`, 1)} {
		if _, e := verify([]byte(bad), sign([]byte(bad)), key); e == nil {
			t.Fatal("invalid signed claims accepted")
		}
	}
	if m.Compatible("0.0.7") || m.Compatible("0.0.10") {
		t.Fatal("source limits bypassed")
	}
}
func TestVersions(t *testing.T) {
	for _, pair := range [][2]string{{"1.10.0", "1.9.9"}, {"2.0.0", "1.99.99"}} {
		if c, e := Compare(pair[0], pair[1]); e != nil || c != 1 {
			t.Fatal(pair, e)
		}
	}
	for _, v := range []string{"01.2.3", "1.2.3-rc1", "1.2.3+build", "1.2", "1.2.1000000000"} {
		if ValidVersion(v) {
			t.Fatal(v)
		}
	}
}

func TestCrossLanguageVector(t *testing.T) {
	b, e := os.ReadFile("testdata/release-vector.json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		PublicKey string `json:"public_key"`
		Manifest  string `json:"manifest"`
		Signature string `json:"signature"`
	}
	if e = Decode(b, &v); e != nil {
		t.Fatal(e)
	}
	key, _ := base64.StdEncoding.DecodeString(v.PublicKey)
	if m, e := verify([]byte(v.Manifest), v.Signature, ed25519.PublicKey(key)); e != nil || m.Version != "0.0.9" {
		t.Fatal(e)
	}
	if _, e = Verify([]byte(v.Manifest), v.Signature); e == nil {
		t.Fatal("fixture key trusted as official")
	}
}
