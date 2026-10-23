package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

//go:embed release-public-key.txt
var publicKeyText string

type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Format           int      `json:"format"`
	Repository       string   `json:"repository"`
	Tag              string   `json:"tag"`
	Version          string   `json:"version"`
	SourceCommit     string   `json:"source_commit"`
	Channel          string   `json:"channel"`
	OS               string   `json:"os"`
	OSVersion        string   `json:"os_version"`
	Architecture     string   `json:"architecture"`
	SourceMin        string   `json:"source_min"`
	SourceMax        string   `json:"source_max"`
	Automatic        bool     `json:"automatic"`
	Capabilities     []string `json:"required_app_capabilities"`
	BinarySHA256     string   `json:"binary_sha256"`
	RuntimeSHA256    string   `json:"runtime_sha256"`
	DescriptorSHA256 string   `json:"descriptor_sha256"`
	Asset            Asset    `json:"asset"`
}
type Descriptor struct {
	Format        int    `json:"format"`
	Version       string `json:"version"`
	SourceCommit  string `json:"source_commit"`
	BinarySHA256  string `json:"binary_sha256"`
	RuntimeSHA256 string `json:"runtime_sha256"`
}

func PublicKey() ed25519.PublicKey {
	b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyText))
	return ed25519.PublicKey(b)
}
func Verify(data []byte, signature string) (Manifest, error) {
	return verify(data, signature, PublicKey())
}
func verify(data []byte, signature string, key ed25519.PublicKey) (Manifest, error) {
	var m Manifest
	s, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(signature))
	if len(data) > 16384 || len(data) == 0 || err != nil || len(s) != 64 || len(key) != 32 || !ed25519.Verify(key, data, s) {
		return m, errors.New("release_signature_invalid")
	}
	if err := Decode(data, &m); err != nil {
		return m, errors.New("release_manifest_invalid")
	}
	if err := m.Validate(); err != nil {
		return m, err
	}
	return m, nil
}
func (m Manifest) Validate() error {
	if m.Format != 1 || m.Repository != Repository || !ValidVersion(m.Version) || m.Tag != "v"+m.Version || m.Channel != "stable" ||
		len(m.SourceCommit) != 40 || m.OS != "debian" || m.OSVersion != "13" || m.Architecture != "amd64" ||
		!ValidDigest(m.BinarySHA256) || !ValidDigest(m.RuntimeSHA256) || !ValidDigest(m.DescriptorSHA256) ||
		m.Asset.Name != "tinywarden-agent-"+m.Version+"-linux-amd64.tar.gz" || m.Asset.Size < 1 || m.Asset.Size > 64<<20 || !ValidDigest(m.Asset.SHA256) {
		return errors.New("release_manifest_invalid")
	}
	if _, err := hex.DecodeString(m.SourceCommit); err != nil {
		return errors.New("release_manifest_invalid")
	}
	order, err := Compare(m.SourceMin, m.SourceMax)
	if err != nil || order > 0 {
		return errors.New("release_manifest_invalid")
	}
	if len(m.Capabilities) < 1 || len(m.Capabilities) > 8 {
		return errors.New("release_manifest_invalid")
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`).MatchString(c) || seen[c] {
			return errors.New("release_manifest_invalid")
		}
		seen[c] = true
	}
	return nil
}
func (m Manifest) Compatible(source string) bool {
	low, e1 := Compare(source, m.SourceMin)
	high, e2 := Compare(source, m.SourceMax)
	return e1 == nil && e2 == nil && low >= 0 && high <= 0
}
func AssetURL(m Manifest) string {
	return "https://github.com/" + Repository + "/releases/download/" + m.Tag + "/" + m.Asset.Name
}
func Digest(data []byte) string { d := sha256.Sum256(data); return hex.EncodeToString(d[:]) }
