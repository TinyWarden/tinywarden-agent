package baseline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

type fixture struct {
	Version    int            `json:"version"`
	Name       string         `json:"name"`
	Key        Key            `json:"key"`
	Mode       string         `json:"mode"`
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at"`
	BootBefore string         `json:"boot_before"`
	BootAfter  string         `json:"boot_after"`
	Outcome    runner.Outcome `json:"outcome"`
	Steps      []struct {
		StepID          string         `json:"step_id"`
		Profile         string         `json:"profile"`
		Outcome         runner.Outcome `json:"outcome"`
		ExitCode        *int           `json:"exit_code"`
		Signal          *int           `json:"signal"`
		Stdout          string         `json:"stdout"`
		Stderr          string         `json:"stderr"`
		StdoutTruncated bool           `json:"stdout_truncated"`
		StderrTruncated bool           `json:"stderr_truncated"`
		CleanupComplete bool           `json:"cleanup_complete"`
	} `json:"steps"`
	Expected   Observation     `json:"expected"`
	Assessment json.RawMessage `json:"assessment"`
}

func TestVersionedNormalizerFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/*/*.json")
	if err != nil || len(files) == 0 {
		t.Fatal("missing versioned fixtures")
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		if json.Unmarshal(data, &f) != nil || f.Version != 1 {
			t.Fatal("bad fixture version")
		}
		t.Run(f.Name, func(t *testing.T) {
			recipe, err := Recipe(f.Key, 30, f.Mode)
			if err != nil {
				t.Fatal(err)
			}
			raw := runner.Result{Outcome: f.Outcome}
			for _, step := range f.Steps {
				raw.Steps = append(raw.Steps, runner.StepResult{ID: step.StepID, Profile: step.Profile, Outcome: step.Outcome, ExitCode: step.ExitCode, Signal: step.Signal, Stdout: []byte(step.Stdout), Stderr: []byte(step.Stderr), StdoutTruncated: step.StdoutTruncated, StderrTruncated: step.StderrTruncated, CleanupComplete: step.CleanupComplete})
			}
			start, err1 := time.Parse(time.RFC3339, f.StartedAt)
			end, err2 := time.Parse(time.RFC3339, f.FinishedAt)
			if err1 != nil || err2 != nil {
				t.Fatal("bad fixture window")
			}
			got, err := Normalize(f.Key, recipe, raw, Window{start, end, f.BootBefore, f.BootAfter})
			if err != nil || !reflect.DeepEqual(got, f.Expected) {
				t.Fatalf("normalizer mismatch: got=%+v expected=%+v err=%v", got, f.Expected, err)
			}
			// JSON is the language boundary, not just Go's in-memory representation.
			encoded, _ := json.Marshal(got)
			wanted, _ := json.Marshal(f.Expected)
			if string(encoded) != string(wanted) {
				t.Fatal("normalized JSON differs from contract fixture")
			}
		})
	}
}
