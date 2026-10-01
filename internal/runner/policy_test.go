package runner

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestWholeRecipeRejectionLaunchesNothing(t *testing.T) {
	valid := string(recipeBytes(1, 2))
	cases := []string{
		strings.Replace(valid, "reboot-marker.v1", "shell.v1", 1),
		strings.Replace(valid, `"-e","/run/reboot-required"`, `"/run/reboot-required","-e"`, 1),
		strings.Replace(valid, `"-e","/run/reboot-required"`, `"-e","/run/reboot-required","extra"`, 1),
		strings.Replace(valid, "/run/reboot-required", "/etc/passwd", 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(valid, `"policy_version":1`, `"policy_version":2`, 1),
		strings.Replace(valid, Capability, "exec_observe.other.v1", 1),
		strings.Replace(valid, `"timeout_seconds":2,`, ``, 1),
		strings.Replace(valid, `"schema_version":1`, `"Schema_Version":1`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(valid, `"step_id":"s0"`, `"step_id":"s0","step_id":"s0"`, 1),
		strings.Replace(valid, `"timeout_seconds":2`, `"timeout_seconds":2.0`, 1),
		strings.Replace(valid, `"timeout_seconds":2`, `"timeout_seconds":31`, 1),
		strings.Replace(valid, `"timeout_seconds":2`, `"timeout_seconds":0`, 1),
		strings.Replace(valid, `"step_id":"s0"`, `"step_id":"s\u00000"`, 1),
		strings.Replace(valid, `"step_id":"s0"`, `"step_id":"S0"`, 1),
		strings.Replace(valid, `"step_id":"s0"`, `"step_id":"`+strings.Repeat("s", 33)+`"`, 1),
		strings.Replace(valid, `"step_id":"s0"`, `"step_id":"`+string([]byte{0xff})+`"`, 1),
		valid + strings.Repeat(" ", MaxRecipeBytes), valid + valid, `{}`, `null`, `[]`,
	}
	for _, field := range []string{"environment", "cwd", "stdin", "user", "executable", "output_limit"} {
		cases = append(cases, strings.Replace(valid, `"step_id":"s0"`, `"`+field+`":null,"step_id":"s0"`, 1))
		cases = append(cases, strings.Replace(valid, `"schema_version":1`, `"`+field+`":null,"schema_version":1`, 1))
	}
	duplicate := string(recipeBytes(2, 2))
	cases = append(cases, strings.Replace(duplicate, `"s1"`, `"s0"`, 1))
	// A later invalid step cannot allow the earlier valid step to execute.
	cases = append(cases, strings.Replace(duplicate, `"step_id":"s1"`, `"step_id":"BAD"`, 1))
	cases = append(cases, string(recipeBytes(5, 2)), string(recipeBytes(0, 2)))
	e := testEngine("success")
	launched := 0
	original := e.supervisor
	e.supervisor = func() *exec.Cmd { launched++; return original() }
	for i, data := range cases {
		result := e.execute(context.Background(), []byte(data))
		if result.Outcome != PolicyRejected || len(result.Steps) != 0 || launched != 0 {
			t.Fatalf("case %d launched=%d outcome=%s", i, launched, result.Outcome)
		}
	}
}

func TestExactProfileArrays(t *testing.T) {
	profiles := []Step{
		{"s", "apt-upgrade.v1", []string{"--simulate", "upgrade"}},
		{"s", "apt-upgrade.v1", []string{"--simulate", "--with-new-pkgs", "upgrade"}},
		{"s", "reboot-marker.v1", []string{"-e", "/run/reboot-required"}},
		{"s", "fstrim-timer.v1", []string{"--system", "--no-pager", "--no-ask-password", "--all", "--timestamp=unix", "show", "--property=Id,LoadState,ActiveState,UnitFileState,LastTriggerUSec,NextElapseUSecRealtime,ConditionResult,ConditionTimestamp", "fstrim.timer"}},
		{"s", "fstrim-service.v1", []string{"--system", "--no-pager", "--no-ask-password", "--all", "--timestamp=unix", "show", "--property=Id,LoadState,ActiveState,Result,ExecMainCode,ExecMainStatus,ExecMainStartTimestamp,ExecMainExitTimestamp,ConditionResult,ConditionTimestamp", "fstrim.service"}},
	}
	for _, profile := range profiles {
		for _, argv := range [][]string{profile.Argv, append(append([]string{}, profile.Argv...), "extra"), profile.Argv[1:]} {
			r := Recipe{1, Capability, 1, 2, []Step{{profile.ID, profile.Profile, argv}}}
			data, _ := json.Marshal(r)
			_, err := Parse(data)
			if (err == nil) != (len(argv) == len(profile.Argv)) {
				t.Fatalf("profile %s: argv=%v", profile.Profile, argv)
			}
		}
	}
}
