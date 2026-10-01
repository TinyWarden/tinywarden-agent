package baseline

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

var errRecipe = errors.New("unsupported baseline recipe")

func Versions(key Key) (normalizer, evaluator string) {
	switch key {
	case Packages:
		return "apt-plan.debian13.v1", "package-plan.v1"
	case Reboot:
		return "reboot-marker.debian13.v1", "reboot-marker.v1"
	case Fstrim:
		return "fstrim-systemd.debian13.v1", "fstrim-systemd.v1"
	}
	return "", ""
}

func DefaultRecipe(key Key) (runner.Recipe, error) {
	seconds := 10
	if key == Packages {
		seconds = 30
	}
	return Recipe(key, seconds, "upgrade")
}

// Only packages has a selectable simulation mode. Other definitions cannot
// compose arbitrary extra steps even when each individual profile is valid.
func Recipe(key Key, seconds int, mode string) (runner.Recipe, error) {
	r := runner.Recipe{SchemaVersion: 1, Capability: runner.Capability, PolicyVersion: 1, TimeoutSeconds: seconds}
	if seconds < 1 || seconds > 30 || (mode != "upgrade" && (key != Packages || mode != "with-new-pkgs")) {
		return r, errRecipe
	}
	switch key {
	case Packages:
		argv := []string{"--simulate", "upgrade"}
		if mode == "with-new-pkgs" {
			argv = []string{"--simulate", "--with-new-pkgs", "upgrade"}
		}
		r.Steps = []runner.Step{{ID: "apt", Profile: "apt-upgrade.v1", Argv: argv}}
	case Reboot:
		r.Steps = []runner.Step{{ID: "marker", Profile: "reboot-marker.v1", Argv: []string{"-e", "/run/reboot-required"}}}
	case Fstrim:
		prefix := []string{"--system", "--no-pager", "--no-ask-password", "--all", "--timestamp=unix", "show"}
		r.Steps = []runner.Step{
			{ID: "timer", Profile: "fstrim-timer.v1", Argv: append(slices.Clone(prefix), "--property=Id,LoadState,ActiveState,UnitFileState,LastTriggerUSec,NextElapseUSecRealtime,ConditionResult,ConditionTimestamp", "fstrim.timer")},
			{ID: "service", Profile: "fstrim-service.v1", Argv: append(slices.Clone(prefix), "--property=Id,LoadState,ActiveState,Result,ExecMainCode,ExecMainStatus,ExecMainStartTimestamp,ExecMainExitTimestamp,ConditionResult,ConditionTimestamp", "fstrim.service")},
		}
	default:
		return r, errRecipe
	}
	return r, nil
}

func recipeMode(key Key, r runner.Recipe) (string, error) {
	encoded, err := json.Marshal(r)
	if err != nil {
		return "", errRecipe
	}
	validated, err := runner.Parse(encoded)
	if err != nil {
		return "", errRecipe
	}
	mode := "upgrade"
	if key == Packages && len(validated.Steps) == 1 && len(validated.Steps[0].Argv) == 3 {
		mode = "with-new-pkgs"
	}
	wanted, err := Recipe(key, r.TimeoutSeconds, mode)
	if err != nil || len(wanted.Steps) != len(r.Steps) {
		return "", errRecipe
	}
	for i, step := range wanted.Steps {
		actual := r.Steps[i]
		if step.ID != actual.ID || step.Profile != actual.Profile || !slices.Equal(step.Argv, actual.Argv) {
			return "", errRecipe
		}
	}
	return mode, nil
}
