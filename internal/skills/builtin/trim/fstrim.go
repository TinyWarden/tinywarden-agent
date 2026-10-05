package trim

import (
	"github.com/TinyWarden/tinywarden-agent/internal/skills/builtin/shared"
	"time"
)

var timerKeys = []string{"Id", "LoadState", "ActiveState", "UnitFileState", "LastTriggerUSec", "NextElapseUSecRealtime", "ConditionResult", "ConditionTimestamp"}
var serviceKeys = []string{"Id", "LoadState", "ActiveState", "Result", "ExecMainCode", "ExecMainStatus", "ExecMainStartTimestamp", "ExecMainExitTimestamp", "ConditionResult", "ConditionTimestamp"}

func parseFstrim(timerData, serviceData []byte, w Window) (*FstrimEvidence, string) {
	// Check wall time independently: time.Time comparisons can use monotonic
	// readings and conceal a backwards wall-clock change during real execution.
	wallElapsed := w.FinishedAt.Round(0).Sub(w.StartedAt.Round(0))
	if w.StartedAt.IsZero() || wallElapsed < 0 || wallElapsed > 31*time.Second || w.FinishedAt.Sub(w.StartedAt) > 31*time.Second || w.StartedAt.Unix() <= 0 || w.FinishedAt.Unix() > MaxEpoch {
		return nil, "clock_uncertain"
	}
	if !bootID.MatchString(w.BootBefore) || w.BootBefore != w.BootAfter {
		return nil, "boot_uncertain"
	}
	timer, ok := properties(timerData, timerKeys)
	if !ok || timer["Id"] != "fstrim.timer" || !states(timer) {
		return nil, "output_unsupported"
	}
	service, ok := properties(serviceData, serviceKeys)
	if !ok || service["Id"] != "fstrim.service" || !states(service) {
		return nil, "output_unsupported"
	}
	if !shared.Member(timer["UnitFileState"], "|enabled|enabled-runtime|linked|linked-runtime|alias|static|indirect|disabled|masked|masked-runtime|generated|transient|bad") ||
		!shared.Member(service["Result"], "success|resources|timeout|exit-code|signal|core-dump|watchdog|start-limit-hit|protocol|exec-condition|oom-kill") {
		return nil, "output_unsupported"
	}
	last, ok1 := timestamp(timer["LastTriggerUSec"])
	next, ok2 := timestamp(timer["NextElapseUSecRealtime"])
	start, ok3 := timestamp(service["ExecMainStartTimestamp"])
	end, ok4 := timestamp(service["ExecMainExitTimestamp"])
	tc, ok5 := condition(timer)
	sc, ok6 := condition(service)
	code, ok7 := boundedNumber(service["ExecMainCode"], 6)
	status, ok8 := boundedNumber(service["ExecMainStatus"], 255)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 || !ok8 {
		return nil, "output_unsupported"
	}
	// Historical fields must predate the non-atomic reads. Same-second events
	// cannot be ordered at this output precision and stay unknown.
	for _, at := range []*int64{last, start, end, tc.CheckedAt, sc.CheckedAt} {
		if at != nil && *at >= w.StartedAt.Unix() {
			if *at > w.FinishedAt.Unix() {
				return nil, "clock_uncertain"
			}
			return nil, "snapshot_changed"
		}
	}
	if start != nil && end != nil && *start > *end {
		return nil, "evidence_inconsistent"
	}
	if next != nil && *next <= w.FinishedAt.Unix() {
		return nil, "evidence_inconsistent"
	}
	if code == 0 && status != 0 || (code == 2 || code == 3) && (status < 1 || status > 64) {
		return nil, "evidence_inconsistent"
	}
	return &FstrimEvidence{
		Timer:      TimerEvidence{LoadState: timer["LoadState"], ActiveState: timer["ActiveState"], UnitFileState: timer["UnitFileState"], LastTrigger: last, NextElapse: next, Condition: tc},
		Service:    ServiceEvidence{LoadState: service["LoadState"], ActiveState: service["ActiveState"], Result: service["Result"], ExitKind: code, ExitStatus: status, StartedAt: start, FinishedAt: end, Condition: sc},
		ObservedAt: w.FinishedAt.Unix(), ReclamationVerified: false,
	}, "none"
}
