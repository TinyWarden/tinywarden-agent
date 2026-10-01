// Package runner enforces the compiled Debian observation policy independently
// of the control plane. It returns execution evidence, never health.
package runner

import (
	"errors"
	"regexp"
	"slices"
)

const (
	Capability         = "exec_observe.debian13.v1"
	MaxRecipeBytes     = 8192
	StdoutLimit        = 64 * 1024
	StderrLimit        = 16 * 1024
	controlLimit       = 4096
	SupervisorArgument = "__observe-exec-v1"
)

var errPolicy = errors.New("observation policy rejected")
var stepID = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

type Step struct {
	ID      string   `json:"step_id"`
	Profile string   `json:"profile"`
	Argv    []string `json:"argv"`
}

type Recipe struct {
	SchemaVersion  int    `json:"schema_version"`
	Capability     string `json:"capability"`
	PolicyVersion  int    `json:"policy_version"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Steps          []Step `json:"steps"`
}

// Parse validates the entire encoded recipe and owns its decoded slices.
func Parse(data []byte) (Recipe, error) {
	var recipe Recipe
	if len(data) > MaxRecipeBytes || decodeStrict(data, &recipe) != nil ||
		recipe.SchemaVersion != 1 || recipe.Capability != Capability ||
		recipe.PolicyVersion != 1 || recipe.TimeoutSeconds < 1 || recipe.TimeoutSeconds > 30 ||
		len(recipe.Steps) < 1 || len(recipe.Steps) > 4 {
		return Recipe{}, errPolicy
	}
	seen := make(map[string]bool)
	for _, step := range recipe.Steps {
		if !stepID.MatchString(step.ID) || seen[step.ID] {
			return Recipe{}, errPolicy
		}
		if _, err := executable(step); err != nil {
			return Recipe{}, errPolicy
		}
		seen[step.ID] = true
	}
	return recipe, nil
}

func executable(step Step) (string, error) {
	if !stepID.MatchString(step.ID) || len(step.Argv) > 16 {
		return "", errPolicy
	}
	for _, arg := range step.Argv {
		if len(arg) > 512 {
			return "", errPolicy
		}
	}
	switch step.Profile {
	case "apt-upgrade.v1":
		if slices.Equal(step.Argv, []string{"--simulate", "upgrade"}) ||
			slices.Equal(step.Argv, []string{"--simulate", "--with-new-pkgs", "upgrade"}) {
			return "/usr/bin/apt-get", nil
		}
	case "reboot-marker.v1":
		if slices.Equal(step.Argv, []string{"-e", "/run/reboot-required"}) {
			return "/usr/bin/test", nil
		}
	case "fstrim-timer.v1", "fstrim-service.v1":
		args := []string{"--system", "--no-pager", "--no-ask-password", "--all", "--timestamp=unix", "show"}
		if step.Profile == "fstrim-timer.v1" {
			args = append(args, "--property=Id,LoadState,ActiveState,UnitFileState,LastTriggerUSec,NextElapseUSecRealtime,ConditionResult,ConditionTimestamp", "fstrim.timer")
		} else {
			args = append(args, "--property=Id,LoadState,ActiveState,Result,ExecMainCode,ExecMainStatus,ExecMainStartTimestamp,ExecMainExitTimestamp,ConditionResult,ConditionTimestamp", "fstrim.service")
		}
		if slices.Equal(step.Argv, args) {
			return "/usr/bin/systemctl", nil
		}
	}
	return "", errPolicy
}
