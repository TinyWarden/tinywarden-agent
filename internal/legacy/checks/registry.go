package checks

import (
	"github.com/TinyWarden/tinywarden-agent/internal/legacy/checks/packages"
	"github.com/TinyWarden/tinywarden-agent/internal/legacy/checks/reboot"
	"github.com/TinyWarden/tinywarden-agent/internal/legacy/checks/trim"
	"github.com/TinyWarden/tinywarden-agent/internal/legacy/runner"
)

type adapter struct {
	normalizer, evaluator string
	timeout               int
	steps                 func(string) []runner.Step
	observe               func(*Observation, runner.Result, Window, string)
}

var adapters = map[Key]adapter{
	Packages: {packages.Normalizer, packages.Evaluator, packages.DefaultTimeout, packages.Steps, packages.Observe},
	Reboot:   {reboot.Normalizer, reboot.Evaluator, reboot.DefaultTimeout, reboot.Steps, reboot.Observe},
	Fstrim:   {trim.Normalizer, trim.Evaluator, trim.DefaultTimeout, trim.Steps, trim.Observe},
}

// Keys preserves the wire order and returns a caller-owned slice.
func Keys() []Key { return []Key{Packages, Reboot, Fstrim} }
