package baseline

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

func Normalize(key Key, recipe runner.Recipe, result runner.Result, window Window) (Observation, error) {
	normalizer, _ := Versions(key)
	mode, err := recipeMode(key, recipe)
	if normalizer == "" || err != nil {
		return Observation{}, errRecipe
	}
	o := Observation{SchemaVersion: 1, Key: key, Normalizer: normalizer, Problem: "none", Execution: []Execution{}}
	for i, step := range result.Steps {
		if i >= len(recipe.Steps) || step.ID != recipe.Steps[i].ID || step.Profile != recipe.Steps[i].Profile {
			o.Problem = "execution_mismatch"
			return o, nil
		}
		if !validExecution(step) {
			o.Problem = "execution_mismatch"
			return o, nil
		}
		o.Execution = append(o.Execution, Execution{step.ID, step.Profile, step.Outcome, copyInt(step.ExitCode), copyInt(step.Signal), step.StdoutTruncated, step.StderrTruncated, step.CleanupComplete})
	}
	if len(result.Steps) != len(recipe.Steps) {
		o.Problem = executionProblem(result.Outcome)
		return o, nil
	}
	for _, step := range result.Steps {
		if step.Outcome != runner.Exited || result.Outcome != runner.Exited || !step.CleanupComplete {
			o.Problem = executionProblem(step.Outcome)
			if result.Outcome != runner.Exited {
				o.Problem = executionProblem(result.Outcome)
			}
			if !step.CleanupComplete {
				o.Problem = "execution_cleanup_pending"
			}
			return o, nil
		}
		if step.StdoutTruncated || step.StderrTruncated || len(step.Stdout) > runner.StdoutLimit || len(step.Stderr) > runner.StderrLimit {
			o.Problem = "execution_output_limit"
			return o, nil
		}
		if step.Signal != nil || step.ExitCode == nil || *step.ExitCode < 0 || *step.ExitCode > 255 || (*step.ExitCode != 0 && (key != Reboot || *step.ExitCode != 1)) {
			o.Problem = "execution_failed"
			return o, nil
		}
		if len(step.Stderr) != 0 || !validText(step.Stdout) {
			o.Problem = "output_unsupported"
			return o, nil
		}
	}
	switch key {
	case Packages:
		o.Packages, o.Problem = parsePackages(result.Steps[0].Stdout, mode)
	case Reboot:
		if len(result.Steps[0].Stdout) != 0 {
			o.Problem = "output_unsupported"
		} else {
			o.Reboot = &RebootEvidence{*result.Steps[0].ExitCode == 0, "unverified"}
		}
	case Fstrim:
		o.Fstrim, o.Problem = parseFstrim(result.Steps[0].Stdout, result.Steps[1].Stdout, window)
	}
	return o, nil
}

func validExecution(step runner.StepResult) bool {
	if !member(string(step.Outcome), "exited|policy_rejected|spawn_failed|timed_out|cancelled|output_limit|helper_failed|cleanup_pending|runner_busy") {
		return false
	}
	return (step.ExitCode == nil || *step.ExitCode >= 0 && *step.ExitCode <= 255) &&
		(step.Signal == nil || *step.Signal >= 1 && *step.Signal <= 64) && !(step.ExitCode != nil && step.Signal != nil)
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func executionProblem(outcome runner.Outcome) string {
	switch outcome {
	case runner.TimedOut:
		return "execution_timed_out"
	case runner.Cancelled:
		return "execution_cancelled"
	case runner.OutputLimit:
		return "execution_output_limit"
	case runner.CleanupPending:
		return "execution_cleanup_pending"
	case runner.PolicyRejected:
		return "execution_policy_rejected"
	case runner.SpawnFailed:
		return "execution_spawn_failed"
	case runner.RunnerBusy:
		return "runner_busy"
	case runner.HelperFailed:
		return "execution_helper_failed"
	}
	return "execution_missing"
}

func validText(data []byte) bool {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	for _, char := range string(data) {
		if (char < 32 && char != '\n' && char != '\t') || char == 127 {
			return false
		}
	}
	return true
}

func lines(data []byte) ([]string, bool) {
	if len(data) == 0 || data[len(data)-1] != '\n' || !validText(data) {
		return nil, false
	}
	return strings.Split(string(data[:len(data)-1]), "\n"), true
}
