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
	ControlPlaneOrigin string `json:"control_plane_origin"`
	StateDir           string `json:"state_dir"`
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
	return config, nil
}
