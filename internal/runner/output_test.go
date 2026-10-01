package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestIndependentExactOutputCaps(t *testing.T) {
	for _, mode := range []string{"out-exact", "err-exact", "both-exact", "out-over", "err-over", "flood"} {
		t.Run(mode, func(t *testing.T) {
			r := testEngine(mode).execute(context.Background(), recipeBytes(2, 3))
			step := r.Steps[0]
			over := mode == "out-over" || mode == "err-over" || mode == "flood"
			wanted := Exited
			if over {
				wanted = OutputLimit
			}
			if r.Outcome != wanted || !step.CleanupComplete {
				t.Fatalf("outcome=%s cleanup=%t", r.Outcome, step.CleanupComplete)
			}
			if len(step.Stdout) > StdoutLimit || len(step.Stderr) > StderrLimit {
				t.Fatal("unbounded evidence")
			}
			if mode == "out-exact" || mode == "out-over" || mode == "both-exact" || mode == "flood" {
				if len(step.Stdout) != StdoutLimit {
					t.Fatal("stdout prefix lost")
				}
			}
			if mode == "err-exact" || mode == "err-over" || mode == "both-exact" {
				if len(step.Stderr) != StderrLimit {
					t.Fatal("stderr prefix lost")
				}
			}
			if step.StdoutTruncated != (mode == "out-over" || mode == "flood") || step.StderrTruncated != (mode == "err-over") {
				t.Fatal("incorrect truncation")
			}
			if over && len(r.Steps) != 1 {
				t.Fatal("step executed after output limit")
			}
			before := append([]byte{}, step.Stdout...)
			time.Sleep(time.Millisecond)
			if string(step.Stdout) != string(before) {
				t.Fatal("returned evidence mutated")
			}
		})
	}
}

func TestRawOutputCannotEnterJSONEvidence(t *testing.T) {
	r := testEngine("raw").execute(context.Background(), recipeBytes(1, 3))
	if r.Outcome != Exited || !bytes.Contains(r.Steps[0].Stdout, []byte("synthetic-secret")) {
		t.Fatal("missing transient fixture output")
	}
	encoded, err := json.Marshal(r)
	if err != nil || bytes.Contains(encoded, []byte("synthetic-secret")) || bytes.Contains(encoded, []byte(`"Stdout":`)) || bytes.Contains(encoded, []byte(`"Stderr":`)) {
		t.Fatal("raw output escaped through JSON")
	}
}
