package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	ControlPlaneOrigin string   `json:"control_plane_origin"`
	StateDir           string   `json:"state_dir"`
	RuntimeAssets      string   `json:"runtime_assets,omitempty"`
	SkillOperations    []string `json:"skill_operations"`
	configPath         string
}

func LoadConfig(path string) (Config, error) {
	var config Config
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return config, errors.New("invalid configuration")
	}
	parsed, err := url.Parse(config.ControlPlaneOrigin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") ||
		strings.TrimSuffix(config.ControlPlaneOrigin, "/") != parsed.Scheme+"://"+parsed.Host ||
		!filepath.IsAbs(config.StateDir) || filepath.Clean(config.StateDir) != config.StateDir {
		return config, errors.New("invalid configuration")
	}
	config.ControlPlaneOrigin = strings.TrimSuffix(config.ControlPlaneOrigin, "/")
	if config.RuntimeAssets == "" {
		config.RuntimeAssets = "/usr/local/lib/tinywarden-agent/runtime"
	}
	if !filepath.IsAbs(config.RuntimeAssets) || filepath.Clean(config.RuntimeAssets) != config.RuntimeAssets {
		return config, errors.New("invalid runtime path")
	}
	if config.SkillOperations == nil {
		config.SkillOperations = []string{"files.read", "files.stat", "files.list", "filesystems.snapshot", "systemd.properties", "command.capture"}
	}
	allowed := map[string]bool{"files.read": true, "files.stat": true, "files.list": true, "filesystems.snapshot": true, "systemd.properties": true, "command.capture": true}
	seen := map[string]bool{}
	for _, operation := range config.SkillOperations {
		if !allowed[operation] || seen[operation] {
			return config, errors.New("invalid skill ceiling")
		}
		seen[operation] = true
	}
	config.configPath, err = filepath.Abs(path)
	if err != nil {
		return config, errors.New("invalid config path")
	}
	return config, nil
}
