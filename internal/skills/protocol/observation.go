// Package protocol converts transient runner output to versioned, allowlisted
// evidence. It has no credential, scheduling, persistence or health ownership.
package protocol

import (
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/runner"
)

type Key string

const (
	Packages Key = "package-updates"
	Reboot   Key = "reboot-required"
	Fstrim   Key = "fstrim-status"
	MaxCount     = 1_000_000
	MaxEpoch     = 253402300799 // Last supported UTC second in year 9999.
)

type Window struct {
	StartedAt, FinishedAt time.Time
	BootBefore, BootAfter string
}

type Execution struct {
	StepID          string         `json:"step_id"`
	Profile         string         `json:"profile"`
	Outcome         runner.Outcome `json:"outcome"`
	ExitCode        *int           `json:"exit_code"`
	Signal          *int           `json:"signal"`
	StdoutTruncated bool           `json:"stdout_truncated"`
	StderrTruncated bool           `json:"stderr_truncated"`
	CleanupComplete bool           `json:"cleanup_complete"`
}

type Observation struct {
	SchemaVersion int              `json:"schema_version"`
	Key           Key              `json:"key"`
	Normalizer    string           `json:"normalizer"`
	Problem       string           `json:"problem"`
	Execution     []Execution      `json:"execution"`
	Packages      *PackageEvidence `json:"packages"`
	Reboot        *RebootEvidence  `json:"reboot"`
	Fstrim        *FstrimEvidence  `json:"fstrim"`
}

type PackageEvidence struct {
	Mode             string `json:"mode"`
	Upgraded         int    `json:"upgraded"`
	Installed        int    `json:"installed"`
	Removed          int    `json:"removed"`
	HeldBack         int    `json:"held_back"`
	IndexFreshness   string `json:"index_freshness"`
	StateConsistency string `json:"state_consistency"`
}

type RebootEvidence struct {
	MarkerObserved bool   `json:"marker_observed"`
	Assurance      string `json:"assurance"`
}

type Condition struct {
	Passed    *bool  `json:"passed"`
	CheckedAt *int64 `json:"checked_at"`
}

type TimerEvidence struct {
	LoadState     string    `json:"load_state"`
	ActiveState   string    `json:"active_state"`
	UnitFileState string    `json:"unit_file_state"`
	LastTrigger   *int64    `json:"last_trigger"`
	NextElapse    *int64    `json:"next_elapse"`
	Condition     Condition `json:"condition"`
}

type ServiceEvidence struct {
	LoadState   string    `json:"load_state"`
	ActiveState string    `json:"active_state"`
	Result      string    `json:"result"`
	ExitKind    int       `json:"exit_kind"`
	ExitStatus  int       `json:"exit_status"`
	StartedAt   *int64    `json:"started_at"`
	FinishedAt  *int64    `json:"finished_at"`
	Condition   Condition `json:"condition"`
}

type FstrimEvidence struct {
	Timer               TimerEvidence   `json:"timer"`
	Service             ServiceEvidence `json:"service"`
	ObservedAt          int64           `json:"observed_at"`
	ReclamationVerified bool            `json:"reclamation_verified"`
}
