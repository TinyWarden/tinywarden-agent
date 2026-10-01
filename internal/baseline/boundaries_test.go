package baseline

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

func cleanPackageResult() runner.Result {
	zero := 0
	return runner.Result{Outcome: runner.Exited, Steps: []runner.StepResult{{
		ID: "apt", Profile: "apt-upgrade.v1", Outcome: runner.Exited, ExitCode: &zero,
		CleanupComplete: true, Stdout: []byte("0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"),
	}}}
}

func TestExecutionBoundariesCannotProducePackageEvidence(t *testing.T) {
	recipe, _ := DefaultRecipe(Packages)
	for _, tc := range []struct {
		name, problem string
		change        func(*runner.Result)
	}{
		{"missing", "execution_missing", func(r *runner.Result) { r.Steps = nil }},
		{"wrong step", "execution_mismatch", func(r *runner.Result) { r.Steps[0].ID = "other" }},
		{"unknown outcome", "execution_mismatch", func(r *runner.Result) { r.Steps[0].Outcome = "other" }},
		{"oversized stdout", "execution_output_limit", func(r *runner.Result) { r.Steps[0].Stdout = bytes.Repeat([]byte("x"), runner.StdoutLimit+1) }},
		{"oversized stderr", "execution_output_limit", func(r *runner.Result) { r.Steps[0].Stderr = bytes.Repeat([]byte("x"), runner.StderrLimit+1) }},
		{"stderr truncated", "execution_output_limit", func(r *runner.Result) { r.Steps[0].StderrTruncated = true }},
		{"cleanup pending", "execution_cleanup_pending", func(r *runner.Result) { r.Steps[0].CleanupComplete = false }},
		{"no exit evidence", "execution_failed", func(r *runner.Result) { r.Steps[0].ExitCode = nil }},
		{"signal", "execution_failed", func(r *runner.Result) { r.Steps[0].ExitCode = nil; n := 15; r.Steps[0].Signal = &n }},
		{"impossible exit", "execution_mismatch", func(r *runner.Result) { n := 256; r.Steps[0].ExitCode = &n }},
		{"contradictory exit", "execution_mismatch", func(r *runner.Result) { n := 9; r.Steps[0].Signal = &n }},
		{"invalid UTF8", "output_unsupported", func(r *runner.Result) { r.Steps[0].Stdout = append([]byte{0xff}, r.Steps[0].Stdout...) }},
		{"NUL", "output_unsupported", func(r *runner.Result) { r.Steps[0].Stdout = append([]byte{0}, r.Steps[0].Stdout...) }},
		{"escape sequence", "output_unsupported", func(r *runner.Result) { r.Steps[0].Stdout = append([]byte{27}, r.Steps[0].Stdout...) }},
		{"no final newline", "output_unsupported", func(r *runner.Result) { r.Steps[0].Stdout = bytes.TrimSuffix(r.Steps[0].Stdout, []byte("\n")) }},
		{"sum overflow", "output_unsupported", func(r *runner.Result) {
			r.Steps[0].Stdout = []byte("1000000 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := cleanPackageResult()
			tc.change(&r)
			o, err := Normalize(Packages, recipe, r, Window{})
			if err != nil || o.Problem != tc.problem || o.Packages != nil || o.Reboot != nil || o.Fstrim != nil {
				t.Fatalf("unsafe execution accepted: %+v, %v", o, err)
			}
		})
	}
	for _, outcome := range []runner.Outcome{runner.PolicyRejected, runner.SpawnFailed, runner.TimedOut,
		runner.Cancelled, runner.OutputLimit, runner.HelperFailed, runner.CleanupPending, runner.RunnerBusy} {
		t.Run(string(outcome), func(t *testing.T) {
			r := cleanPackageResult()
			r.Outcome = outcome
			o, err := Normalize(Packages, recipe, r, Window{})
			if err != nil || o.Problem != executionProblem(outcome) || o.Packages != nil {
				t.Fatalf("overall failure became a plan: %+v, %v", o, err)
			}
		})
	}
}

func TestNormalizedEvidenceOwnsStatusAndDropsRawData(t *testing.T) {
	recipe, _ := DefaultRecipe(Packages)
	r := cleanPackageResult()
	r.Steps[0].Stdout = append([]byte("https://synthetic.invalid/private-token\n"), r.Steps[0].Stdout...)
	o, err := Normalize(Packages, recipe, r, Window{})
	if err != nil || o.Packages == nil {
		t.Fatal(o, err)
	}
	*r.Steps[0].ExitCode = 100
	r.Steps[0].Stdout[0] = '!'
	encoded, err := json.Marshal(o)
	if err != nil || bytes.Contains(encoded, []byte("synthetic.invalid")) || bytes.Contains(encoded, []byte("stdout\"")) || *o.Execution[0].ExitCode != 0 {
		t.Fatal("raw data or mutable status entered observation", string(encoded), err)
	}
}
