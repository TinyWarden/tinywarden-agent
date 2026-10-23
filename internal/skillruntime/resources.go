package skillruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// ResourcesReady verifies the pinned SDK 2 parser through the enforced sandbox.
func (c Client) ResourcesReady(ctx context.Context) bool {
	if c.VerifyAssets() != nil {
		return false
	}
	data, e := os.ReadFile(filepath.Join(c.Assets, "resource_probe", "skill.json"))
	if e != nil {
		return false
	}
	var manifest struct {
		Defaults json.RawMessage `json:"defaults"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	inspected, e := c.Invoke(ctx, Request{Action: "inspect", Package: filepath.Join(c.Assets, "resource_probe")})
	if e != nil {
		return false
	}
	var metadata struct {
		Digest string `json:"content_sha256"`
	}
	if json.Unmarshal(inspected, &metadata) != nil || len(metadata.Digest) != 64 {
		return false
	}
	out, e := c.Invoke(ctx, Request{Action: "run", Package: filepath.Join(c.Assets, "resource_probe"), ContentSHA256: metadata.Digest, Function: "validate_settings", Arguments: manifest.Defaults})
	if e != nil {
		return false
	}
	var response Response
	return json.Unmarshal(out, &response) == nil && string(response.Result) == "[]" && response.ContentSHA256 == metadata.Digest
}
