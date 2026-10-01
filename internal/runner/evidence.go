package runner

import "time"

type Outcome string

const (
	Exited         Outcome = "exited"
	PolicyRejected Outcome = "policy_rejected"
	SpawnFailed    Outcome = "spawn_failed"
	TimedOut       Outcome = "timed_out"
	Cancelled      Outcome = "cancelled"
	OutputLimit    Outcome = "output_limit"
	HelperFailed   Outcome = "helper_failed"
	CleanupPending Outcome = "cleanup_pending"
	RunnerBusy     Outcome = "runner_busy"
)

// Raw buffers are transient execution evidence and excluded from JSON. They
// must never be stored, sent to the server or included in diagnostics.
type StepResult struct {
	ID              string
	Profile         string
	Duration        time.Duration
	Outcome         Outcome
	ExitCode        *int
	Signal          *int
	Stdout          []byte `json:"-"`
	Stderr          []byte `json:"-"`
	StdoutTruncated bool
	StderrTruncated bool
	CleanupComplete bool
}

type Result struct {
	Outcome Outcome
	Steps   []StepResult
}

type request struct {
	Step   Step  `json:"step"`
	Budget int64 `json:"budget_ns"`
}

type completion struct {
	Outcome  Outcome `json:"outcome"`
	ExitCode *int    `json:"exit_code"`
	Signal   *int    `json:"signal"`
}

func validCompletion(c completion) bool {
	if c.Outcome == PolicyRejected || c.Outcome == SpawnFailed {
		return c.ExitCode == nil && c.Signal == nil
	}
	if c.Outcome != Exited || (c.ExitCode == nil) == (c.Signal == nil) {
		return false
	}
	return (c.ExitCode != nil && *c.ExitCode >= 0 && *c.ExitCode <= 255) ||
		(c.Signal != nil && *c.Signal >= 1 && *c.Signal <= 64)
}
