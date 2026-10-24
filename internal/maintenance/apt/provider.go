package apt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

func (p *Provider) facts() map[string]any {
	facts := map[string]any{"blocked_subjects": []string{}, "blocked_subject_count": 0}
	if p.Action == "packages.upgrade_conservative.v1" {
		facts["reboot_required"] = nil
		facts["configuration_review"] = nil
		facts["services_restart_required"] = nil
		facts["kept_back"] = nil
	}
	return facts
}
func Registry(base string, guard *os.File, verify func() error) maintenance.Registry {
	registry := maintenance.Registry{}
	for _, action := range []string{"packages.refresh.v1", "packages.upgrade_conservative.v1"} {
		provider := &Provider{Base: base, Action: action, Guard: guard, Verify: verify}
		facts, _ := json.Marshal(provider.facts())
		registry = append(registry, maintenance.Definition{ID: action, Version: 1, Approval: action != "packages.refresh.v1", New: func() maintenance.Provider { return provider }, ErrorFacts: facts})
	}
	return registry
}
func (p *Provider) Prepare(ctx context.Context) (maintenance.Prepared, error) {
	phase := "prepare"
	if p.Action == "packages.refresh.v1" {
		phase = "refresh"
	}
	reply, e := p.run(ctx, phase, nil, nil)
	if e != nil {
		return maintenance.Prepared{}, e
	}
	if phase == "refresh" {
		var result struct {
			Outcome string `json:"outcome"`
			Effect  string `json:"effect"`
		}
		if controlplane.Decode(reply.Result, &result) != nil || result.Outcome != "indexes_refreshed" || result.Effect != "indexes_changed" {
			return maintenance.Prepared{}, errors.New("maintenance_result_invalid")
		}
		facts, _ := json.Marshal(p.facts())
		return maintenance.Prepared{Effect: result.Effect, Result: &maintenance.Result{Effect: result.Effect, LocalVerified: true, Facts: facts}}, nil
	}
	var prepared struct {
		Plan json.RawMessage `json:"plan"`
		SHA  string          `json:"sha256"`
	}
	if len(reply.Result) > maintenance.MaxPlan+maintenance.MaxControl || controlplane.DecodeArtifact(reply.Result, &prepared, 180000) != nil || len(prepared.Plan) > maintenance.MaxPlan {
		return maintenance.Prepared{}, errors.New("maintenance_plan_invalid")
	}
	sum := sha256.Sum256(prepared.Plan)
	if hex.EncodeToString(sum[:]) != prepared.SHA {
		return maintenance.Prepared{}, errors.New("maintenance_plan_invalid")
	}
	return maintenance.Prepared{Plan: prepared.Plan, Digest: prepared.SHA, Effect: "indexes_changed"}, nil
}
func (p *Provider) Fetch(ctx context.Context, expected string) error {
	reply, e := p.run(ctx, "fetch", &expected, nil)
	if e != nil {
		return e
	}
	var result struct {
		SHA        string `json:"sha256"`
		Downloaded bool   `json:"downloaded"`
	}
	if controlplane.Decode(reply.Result, &result) != nil || result.SHA != expected || !result.Downloaded {
		return errors.New("maintenance_result_invalid")
	}
	return nil
}

type verifyProof struct {
	StateSHA     string `json:"state_sha256"`
	SelectionSHA string `json:"selection_sha256"`
}
type appliedResult struct {
	Outcome       string `json:"outcome"`
	Effect        string `json:"effect"`
	Reboot        bool   `json:"reboot_required"`
	Configuration *bool  `json:"configuration_review"`
	Services      *bool  `json:"services_restart_required"`
	StateSHA      string `json:"state_sha256"`
	SelectionSHA  string `json:"selection_sha256"`
}

func (p *Provider) applied(reply nativeReply) (maintenance.Result, error) {
	var value appliedResult
	if controlplane.Decode(reply.Result, &value) != nil || value.Outcome != "packages_applied" || value.Effect != "packages_changed" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value.StateSHA) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value.SelectionSHA) {
		return maintenance.Result{}, errors.New("maintenance_result_invalid")
	}
	if p.Verify != nil {
		if e := p.Verify(); e != nil {
			return maintenance.Result{}, e
		}
	}
	facts := p.facts()
	facts["reboot_required"] = value.Reboot
	facts["configuration_review"] = value.Configuration
	facts["services_restart_required"] = value.Services
	if plan, e := maintenance.ReadPlan(p.Base); e == nil {
		var v struct {
			KeptBack []json.RawMessage `json:"kept_back"`
		}
		if json.Unmarshal(plan, &v) == nil {
			facts["kept_back"] = len(v.KeptBack)
		}
	}
	b, _ := json.Marshal(facts)
	return maintenance.Result{Effect: value.Effect, LocalVerified: true, Facts: b}, nil
}
func (p *Provider) Apply(expected string, start maintenance.Start) (maintenance.Result, error) {
	reply, e := p.run(context.Background(), "apply", &expected, &start)
	if e != nil {
		return maintenance.Result{}, e
	}
	return p.applied(reply)
}
func (p *Provider) Recover(expected string, start maintenance.Start) (maintenance.Result, error) {
	r, e := p.binding("apply", &expected, &start)
	if e != nil {
		return maintenance.Result{}, e
	}
	reply, e := p.read(r)
	if e != nil {
		if _, ok := e.(providerError); ok {
			return maintenance.Result{}, e
		}
		reason := "maintenance_receipt_incomplete"
		facts, _ := json.Marshal(p.facts())
		return maintenance.Result{}, providerError{maintenance.Result{Effect: "unknown", Reason: &reason, ProcessRunning: true, Facts: facts}}
	}
	var applied appliedResult
	if controlplane.Decode(reply.Result, &applied) != nil {
		return maintenance.Result{}, errors.New("maintenance_receipt_invalid")
	}
	p.proof = &verifyProof{applied.StateSHA, applied.SelectionSHA}
	defer func() { p.proof = nil }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	verification, e := p.run(ctx, "verify", &expected, nil)
	if e != nil {
		return maintenance.Result{}, e
	}
	var proof struct {
		Verified bool `json:"verified"`
	}
	if controlplane.Decode(verification.Result, &proof) != nil || !proof.Verified {
		return maintenance.Result{}, errors.New("maintenance_verification_failed")
	}
	return p.applied(reply)
}
