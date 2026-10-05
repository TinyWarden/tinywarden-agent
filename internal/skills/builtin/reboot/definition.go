package reboot

import (
	"github.com/TinyWarden/tinywarden-agent/internal/runner"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/protocol"
)

const Normalizer = "reboot-marker.debian13.v1"
const Evaluator = "reboot-marker.v1"
const DefaultTimeout = 10

func Steps(_ string) []runner.Step {
	return []runner.Step{{ID: "marker", Profile: "reboot-marker.v1", Argv: []string{"-e", "/run/reboot-required"}}}
}
func Observe(o *protocol.Observation, result runner.Result, _ protocol.Window, _ string) {
	if len(result.Steps[0].Stdout) != 0 {
		o.Problem = "output_unsupported"
	} else {
		o.Reboot = &protocol.RebootEvidence{MarkerObserved: *result.Steps[0].ExitCode == 0, Assurance: "unverified"}
	}
}
