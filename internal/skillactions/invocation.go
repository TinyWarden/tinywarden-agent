package skillactions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

type Provider struct {
	Loader  Loader
	Intent  maintenance.Intent
	Package string
}
type invocation struct {
	Schema       int                     `json:"schema_version"`
	Binding      *actioncontract.Binding `json:"binding"`
	Scope        maintenance.Scope       `json:"scope"`
	JobID        string                  `json:"job_id"`
	Phase        string                  `json:"phase"`
	InputJSON    string                  `json:"input_json"`
	SettingsJSON string                  `json:"settings_json"`
	PlanJSON     *string                 `json:"plan_json"`
	PlanSHA      *string                 `json:"plan_sha256"`
	ReceiptJSON  *string                 `json:"receipt_json"`
	Start        *maintenance.Start      `json:"start"`
}
type reply struct {
	Schema  int                     `json:"schema_version"`
	Binding *actioncontract.Binding `json:"binding"`
	Scope   maintenance.Scope       `json:"scope"`
	JobID   string                  `json:"job_id"`
	Phase   string                  `json:"phase"`
	Result  json.RawMessage         `json:"result"`
}
type packagePlan struct {
	Plan            json.RawMessage `json:"plan"`
	HasChanges      bool            `json:"has_changes"`
	NoChangesResult *packageResult  `json:"no_changes_result"`
}

func sameIntent(a, b maintenance.Intent) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func (p *Provider) request(phase string, expected *string, start *maintenance.Start, receipt *string) (invocation, error) {
	i := p.Intent
	if i.Binding == nil || i.Validate() != nil || p.Loader.Verify == nil || p.Loader.Guard == nil {
		return invocation{}, errors.New("action_binding_invalid")
	}
	if err := p.Loader.Verify(); err != nil {
		return invocation{}, err
	}
	if err := p.Loader.Runtime.VerifyAssets(); err != nil {
		return invocation{}, err
	}
	value := invocation{Schema: 1, Binding: i.Binding, Scope: i.Scope, JobID: i.ID, Phase: phase, InputJSON: i.InputJSON, SettingsJSON: i.SettingsJSON, Start: start, ReceiptJSON: receipt}
	if phase != "inspect" {
		journal, err := maintenance.Load(p.Loader.Base)
		if err != nil || !sameIntent(journal.Intent, i) {
			return invocation{}, errors.New("action_binding_invalid")
		}
		if start != nil && (journal.Start == nil || *journal.Start != *start || journal.ApplyAttempt == nil) {
			return invocation{}, errors.New("maintenance_start_invalid")
		}
	}
	if expected != nil {
		raw, err := maintenance.ReadPlan(p.Loader.Base)
		if err != nil || digest(raw) != *expected {
			return invocation{}, errors.New("maintenance_plan_changed")
		}
		var plan packagePlan
		if controlplane.DecodeArtifact(raw, &plan, 180000) != nil || !json.Valid(plan.Plan) {
			return invocation{}, errors.New("maintenance_plan_invalid")
		}
		text, sha := string(plan.Plan), digest(plan.Plan)
		value.PlanJSON = &text
		value.PlanSHA = &sha
	}
	return value, nil
}

func (p *Provider) command(ctx context.Context, request invocation, output *os.File, notify *os.File) (*exec.Cmd, error) {
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > 17<<20 {
		return nil, errors.New("maintenance_input_invalid")
	}
	state := filepath.Join(p.Loader.Base, "updater", "private", "action-state-"+p.Intent.ID)
	arguments := []string{"-I", "-B", filepath.Join(p.Loader.Runtime.Assets, "actions", "runner.py"), p.Package, p.Loader.Policy, state}
	var cmd *exec.Cmd
	if request.Phase == "apply" || request.Phase == "inspect" {
		cmd = exec.Command("/usr/bin/python3", arguments...)
	} else {
		cmd = exec.CommandContext(ctx, "/usr/bin/python3", arguments...)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	}
	cmd.Stdin = bytes.NewReader(append(raw, '\n'))
	cmd.Stdout = output
	cmd.ExtraFiles = []*os.File{p.Loader.Guard}
	if notify != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, notify)
	}
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC"}
	return cmd, nil
}

func (p *Provider) read(request invocation) (json.RawMessage, error) {
	raw, err := maintenance.ReadArtifact(p.Loader.Base, p.Intent.ID, "action_"+request.Phase, 9<<20)
	if err != nil {
		return nil, err
	}
	var value reply
	if controlplane.DecodeArtifact(raw, &value, 180000) != nil || value.Schema != 1 || value.JobID != request.JobID || value.Scope != request.Scope || value.Phase != request.Phase || !actioncontract.Equal(value.Binding, request.Binding) {
		return nil, errors.New("maintenance_receipt_invalid")
	}
	var failure struct {
		Failure *packageResult `json:"failure"`
	}
	if json.Unmarshal(value.Result, &failure) == nil && failure.Failure != nil {
		if controlplane.Decode(value.Result, &failure) != nil {
			return nil, errors.New("maintenance_receipt_invalid")
		}
		return nil, outcomeError{failure.Failure.maintenanceResult()}
	}
	return value.Result, nil
}

func (p *Provider) run(ctx context.Context, request invocation) (json.RawMessage, error) {
	var output *os.File
	var err error
	if request.Phase == "apply" {
		path, e := maintenance.ArtifactPath(p.Loader.Base, p.Intent.ID, "action_apply")
		if e != nil {
			return nil, e
		}
		output, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	} else {
		output, err = maintenance.CreateArtifact(p.Loader.Base, p.Intent.ID, "action_"+request.Phase)
	}
	if err != nil {
		return nil, err
	}
	defer output.Close()
	cmd, err := p.command(ctx, request, output, nil)
	if err != nil {
		return nil, err
	}
	stopLogs := p.watchLogs()
	execution := cmd.Run()
	stopLogs()
	raw, err := p.read(request)
	if err != nil {
		return nil, err
	}
	if execution != nil {
		return nil, errors.New("maintenance_action_failed")
	}
	return raw, nil
}
