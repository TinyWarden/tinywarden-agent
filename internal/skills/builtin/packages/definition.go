package packages

import (
	"github.com/TinyWarden/tinywarden-agent/internal/runner"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/protocol"
)

const Normalizer = "apt-plan.debian13.v1"
const Evaluator = "package-plan.v1"
const DefaultTimeout = 30

func Steps(mode string) []runner.Step {
	argv := []string{"--simulate", "upgrade"}
	if mode == "with-new-pkgs" {
		argv = []string{"--simulate", "--with-new-pkgs", "upgrade"}
	}
	return []runner.Step{{ID: "apt", Profile: "apt-upgrade.v1", Argv: argv}}
}
func Observe(o *protocol.Observation, result runner.Result, _ protocol.Window, mode string) {
	o.Packages, o.Problem = parsePackages(result.Steps[0].Stdout, mode)
}
