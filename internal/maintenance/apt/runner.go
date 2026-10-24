package apt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

type receipt struct {
	JobID       string  `json:"job_id"`
	AttemptID   *string `json:"attempt_id"`
	BootID      string  `json:"boot_id"`
	ProviderSHA *string `json:"provider_sha256"`
	Phase       string  `json:"phase"`
}
type nativeReply struct {
	Schema       int             `json:"schema_version"`
	Receipt      receipt         `json:"receipt"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *string         `json:"error,omitempty"`
	Subjects     []string        `json:"subjects,omitempty"`
	SubjectCount int             `json:"subject_count,omitempty"`
	Effect       string          `json:"effect,omitempty"`
}
type Provider struct {
	Base, Action string
	Guard        *os.File
	Verify       func() error
	proof        *verifyProof
}
type providerError struct{ result maintenance.Result }

func (e providerError) Error() string                         { return *e.result.Reason }
func (e providerError) MaintenanceResult() maintenance.Result { return e.result }

func (p *Provider) binding(phase string, expected *string, start *maintenance.Start) (receipt, error) {
	j, e := maintenance.Load(p.Base)
	if e != nil {
		return receipt{}, e
	}
	if j.Intent.Action != p.Action {
		return receipt{}, errors.New("maintenance_scope_changed")
	}
	boot, _, e := maintenance.BootClock()
	if e != nil {
		return receipt{}, e
	}
	r := receipt{JobID: j.Intent.ID, BootID: boot, ProviderSHA: expected, Phase: phase}
	if start != nil {
		if j.Start == nil || *j.Start != *start || j.ApplyAttempt == nil {
			return receipt{}, errors.New("maintenance_start_invalid")
		}
		r.AttemptID = &start.AttemptID
		r.BootID = start.BootID
	}
	return r, nil
}
func (p *Provider) read(r receipt) (nativeReply, error) {
	raw, e := maintenance.ReadArtifact(p.Base, r.JobID, r.Phase, maintenance.MaxPlan+maintenance.MaxControl)
	if e != nil {
		return nativeReply{}, e
	}
	var reply nativeReply
	if controlplane.DecodeArtifact(raw, &reply, 180000) != nil || reply.Schema != 1 {
		return nativeReply{}, errors.New("maintenance_receipt_incomplete")
	}
	expected, _ := json.Marshal(r)
	actual, _ := json.Marshal(reply.Receipt)
	if !bytes.Equal(expected, actual) {
		return nativeReply{}, errors.New("maintenance_scope_changed")
	}
	if reply.Error != nil {
		if !regexp.MustCompile(`^maintenance_[a-z_]{1,64}$`).MatchString(*reply.Error) || reply.SubjectCount < len(reply.Subjects) || len(reply.Subjects) > 5000 {
			return nativeReply{}, errors.New("maintenance_receipt_invalid")
		}
		facts := p.facts()
		subjects := make([]string, 0, 32)
		for _, s := range reply.Subjects {
			if len(subjects) == 32 {
				break
			}
			if len(s) > 288 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.:@/-]*$`).MatchString(s) {
				return nativeReply{}, errors.New("maintenance_receipt_invalid")
			}
			subjects = append(subjects, s)
		}
		facts["blocked_subjects"] = subjects
		facts["blocked_subject_count"] = reply.SubjectCount
		b, _ := json.Marshal(facts)
		effect := reply.Effect
		if effect != "none" && effect != "indexes_changed" && effect != "packages_changed" && effect != "unknown" {
			return nativeReply{}, errors.New("maintenance_receipt_invalid")
		}
		return reply, providerError{maintenance.Result{Effect: effect, Reason: reply.Error, Facts: b}}
	}
	if len(reply.Result) == 0 {
		return nativeReply{}, errors.New("maintenance_receipt_incomplete")
	}
	return reply, nil
}
func (p *Provider) run(ctx context.Context, phase string, expected *string, start *maintenance.Start) (nativeReply, error) {
	r, e := p.binding(phase, expected, start)
	if e != nil {
		return nativeReply{}, e
	}
	if p.Guard == nil {
		return nativeReply{}, errors.New("maintenance_guard_required")
	}
	if p.Verify != nil {
		if e = p.Verify(); e != nil {
			return nativeReply{}, e
		}
	}
	file, e := maintenance.CreateArtifact(p.Base, r.JobID, phase)
	if e != nil {
		return nativeReply{}, e
	}
	defer file.Close()
	source, e := Source()
	if e != nil {
		return nativeReply{}, e
	}
	scope, _ := json.Marshal(r)
	literal, _ := json.Marshal(string(scope))
	// Only trusted typed scope enters this literal. No user script/import path/argv.
	code := "import json\nMAINTENANCE_RECEIPT = json.loads(" + string(literal) + ")\n" + source
	if p.proof != nil {
		proof, _ := json.Marshal(p.proof)
		literal, _ := json.Marshal(string(proof))
		code = "import json\nMAINTENANCE_VERIFY = json.loads(" + string(literal) + ")\n" + code
	}
	request, _ := json.Marshal(struct {
		Phase   string             `json:"phase"`
		PlanSHA *string            `json:"plan_sha256"`
		Start   *maintenance.Start `json:"start"`
	}{phase, expected, start})
	var cmd *exec.Cmd
	if phase == "apply" {
		cmd = exec.Command("/usr/bin/python3", "-I", "-B", "-c", code)
	} else {
		cmd = exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", code)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	}
	cmd.Stdin = bytes.NewReader(request)
	cmd.Stdout = file
	cmd.Stderr = nil
	cmd.ExtraFiles = []*os.File{p.Guard}
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC"}
	cmd.Dir = "/"
	execution := cmd.Run()
	reply, e := p.read(r)
	if e != nil {
		return nativeReply{}, e
	}
	if execution != nil {
		return nativeReply{}, errors.New("maintenance_adapter_failed")
	}
	return reply, nil
}

// Prerequisites are optional for monitoring/updates. This imports no skill code.
func Prerequisites() []string {
	missing := []string{}
	if info, e := os.Stat("/usr/bin/apt-get"); e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		missing = append(missing, "apt-get")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", "import apt,apt_pkg")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C.UTF-8"}
	cmd.Dir = "/"
	if cmd.Run() != nil {
		missing = append(missing, "python3-apt")
	}
	return missing
}
