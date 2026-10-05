package builtin

import (
	"github.com/TinyWarden/tinywarden-agent/internal/runner"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/builtin/packages"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/builtin/reboot"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/builtin/trim"
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
