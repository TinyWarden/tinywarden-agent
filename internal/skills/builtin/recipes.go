package builtin

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/TinyWarden/tinywarden-agent/internal/runner"
)

var errRecipe = errors.New("unsupported baseline recipe")

func Versions(key Key) (normalizer, evaluator string) {
	definition, ok := adapters[key]
	if !ok {
		return "", ""
	}
	return definition.normalizer, definition.evaluator
}
func DefaultRecipe(key Key) (runner.Recipe, error) {
	definition, ok := adapters[key]
	if !ok {
		return Recipe(key, 10, "upgrade")
	}
	return Recipe(key, definition.timeout, "upgrade")
}

// Whole-recipe validation remains separate from skill-owned recipe construction.
func Recipe(key Key, seconds int, mode string) (runner.Recipe, error) {
	r := runner.Recipe{SchemaVersion: 1, Capability: runner.Capability, PolicyVersion: 1, TimeoutSeconds: seconds}
	definition, ok := adapters[key]
	if !ok || seconds < 1 || seconds > 30 || (mode != "upgrade" && (key != Packages || mode != "with-new-pkgs")) {
		return r, errRecipe
	}
	r.Steps = definition.steps(mode)
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
