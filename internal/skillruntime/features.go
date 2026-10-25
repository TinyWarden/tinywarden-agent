package skillruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const SDK3Feature = "skill_packages.sdk3.v1"

func (c Client) SupportsSDK3() bool {
	if c.VerifyAssets() != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(c.Assets, "artifact.json"))
	if err != nil {
		return false
	}
	var artifact struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &artifact) != nil {
		return false
	}
	for _, name := range []string{"actions/descriptor.py", "actions/envelope.py", "actions/runner.py", "skill_actions.py"} {
		if artifact.Files[name] == "" {
			return false
		}
	}
	return true
}
