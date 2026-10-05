package trim

import (
	"github.com/TinyWarden/tinywarden-agent/internal/runner"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/protocol"
	"slices"
)

const Normalizer = "fstrim-systemd.debian13.v1"
const Evaluator = "fstrim-systemd.v1"
const DefaultTimeout = 10

func Steps(_ string) []runner.Step {
	prefix := []string{"--system", "--no-pager", "--no-ask-password", "--all", "--timestamp=unix", "show"}
	return []runner.Step{
		{ID: "timer", Profile: "fstrim-timer.v1", Argv: append(slices.Clone(prefix), "--property=Id,LoadState,ActiveState,UnitFileState,LastTriggerUSec,NextElapseUSecRealtime,ConditionResult,ConditionTimestamp", "fstrim.timer")},
		{ID: "service", Profile: "fstrim-service.v1", Argv: append(slices.Clone(prefix), "--property=Id,LoadState,ActiveState,Result,ExecMainCode,ExecMainStatus,ExecMainStartTimestamp,ExecMainExitTimestamp,ConditionResult,ConditionTimestamp", "fstrim.service")},
	}
}
func Observe(o *protocol.Observation, result runner.Result, window protocol.Window, _ string) {
	o.Fstrim, o.Problem = parseFstrim(result.Steps[0].Stdout, result.Steps[1].Stdout, window)
}
