package skillactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

type packageResult struct {
	Effect         string                        `json:"effect"`
	Reason         *actioncontract.PackageReason `json:"reason"`
	LocalVerified  bool                          `json:"local_verified"`
	ProcessRunning bool                          `json:"process_running"`
	Facts          json.RawMessage               `json:"facts"`
	ReceiptJSON    *string                       `json:"receipt_json"`
}

func (v packageResult) maintenanceResult() maintenance.Result {
	result := maintenance.Result{Effect: v.Effect, LocalVerified: v.LocalVerified, ProcessRunning: v.ProcessRunning, Facts: v.Facts, ReasonMessage: v.Reason}
	if v.Reason != nil {
		reason := "maintenance_action_failed"
		result.Reason = &reason
	}
	return result
}

type outcomeError struct{ result maintenance.Result }

func (e outcomeError) Error() string {
	if e.result.Reason != nil {
		return *e.result.Reason
	}
	return "maintenance_action_failed"
}
func (e outcomeError) MaintenanceResult() maintenance.Result { return e.result }

type bounded struct {
	bytes.Buffer
	limit int
}

func (b *bounded) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > b.limit {
		return 0, errors.New("output_exceeded")
	}
	return b.Buffer.Write(raw)
}

func (p *Provider) probe(ctx context.Context, request invocation) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", filepath.Join(p.Loader.Runtime.Assets, "actions", "probe.py"), p.Package, p.Loader.Policy)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC"}
	output := &bounded{limit: maintenance.MaxControl}
	cmd.Stdout = output
	var value struct {
		Ready   bool     `json:"ready"`
		Reason  *string  `json:"reason"`
		Missing []string `json:"missing"`
	}
	if cmd.Run() != nil || controlplane.Decode(output.Bytes(), &value) != nil {
		return errors.New("maintenance_action_unavailable")
	}
	if value.Ready {
		return nil
	}
	reason := "maintenance_action_unavailable"
	if value.Reason != nil && (*value.Reason == "action_permission_required" || *value.Reason == "action_not_authorized") {
		reason = "maintenance_action_permission_required"
	}
	if value.Reason != nil && *value.Reason == "action_prerequisites" {
		reason = "maintenance_prerequisites"
	}
	return outcomeError{maintenance.Result{Effect: "none", Reason: &reason, Facts: json.RawMessage(`{}`)}}
}

func (p *Provider) Prepare(ctx context.Context) (maintenance.Prepared, error) {
	request, err := p.request("prepare", nil, nil, nil)
	if err != nil {
		return maintenance.Prepared{}, err
	}
	if err = p.probe(ctx, request); err != nil {
		return maintenance.Prepared{}, err
	}
	raw, err := p.run(ctx, request)
	if err != nil {
		return maintenance.Prepared{}, err
	}
	var value struct {
		PlanJSON   string         `json:"plan_json"`
		HasChanges bool           `json:"has_changes"`
		Effect     string         `json:"effect"`
		Result     *packageResult `json:"result"`
	}
	if controlplane.DecodeArtifact(raw, &value, 180000) != nil || len(value.PlanJSON) > maintenance.MaxPlan || !json.Valid([]byte(value.PlanJSON)) {
		return maintenance.Prepared{}, errors.New("maintenance_plan_invalid")
	}
	// Preserve exact domain-plan bytes inside the generic reviewed envelope.
	noChanges, err := json.Marshal(value.Result)
	if err != nil {
		return maintenance.Prepared{}, err
	}
	flag := "false"
	if value.HasChanges {
		flag = "true"
	}
	plan := []byte(`{"has_changes":` + flag + `,"plan":` + value.PlanJSON + `,"no_changes_result":` + string(noChanges) + `}`)
	if len(plan) > maintenance.MaxPlan {
		return maintenance.Prepared{}, errors.New("maintenance_plan_invalid")
	}
	return maintenance.Prepared{Plan: plan, Digest: digest(plan), NoChanges: !value.HasChanges, Effect: value.Effect}, nil
}
func (p *Provider) Fetch(ctx context.Context, expected string) error {
	request, err := p.request("fetch", &expected, nil, nil)
	if err != nil {
		return err
	}
	_, err = p.run(ctx, request)
	return err
}
func (p *Provider) Apply(expected string, start maintenance.Start) (maintenance.Result, error) {
	request, err := p.request("apply", &expected, &start, nil)
	if err != nil {
		return maintenance.Result{}, err
	}
	if err = p.probe(context.Background(), request); err != nil {
		return maintenance.Result{}, err
	}
	raw, err := p.run(context.Background(), request)
	if os.IsExist(err) {
		return p.Recover(expected, start)
	}
	if err != nil {
		return maintenance.Result{}, err
	}
	var value packageResult
	if controlplane.Decode(raw, &value) != nil {
		return maintenance.Result{}, errors.New("maintenance_result_invalid")
	}
	return value.maintenanceResult(), nil
}
func (p *Provider) Recover(expected string, start maintenance.Start) (maintenance.Result, error) {
	request, err := p.request("apply", &expected, &start, nil)
	if err != nil {
		return maintenance.Result{}, err
	}
	raw, err := p.read(request)
	if err != nil {
		return maintenance.Result{}, err
	}
	var value packageResult
	if controlplane.Decode(raw, &value) != nil || value.ReceiptJSON == nil {
		return maintenance.Result{}, errors.New("maintenance_receipt_invalid")
	}
	verification, err := p.request("verify", &expected, nil, value.ReceiptJSON)
	if err != nil {
		return maintenance.Result{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err = p.run(ctx, verification); err != nil {
		return maintenance.Result{}, err
	}
	return value.maintenanceResult(), nil
}
