// Package maintenance coordinates typed root actions independently of monitoring.
package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

const MaxEvents = 256
const Capability = "maintenance_jobs.v1"
const MaxPlan = 4 * 1024 * 1024
const MaxControl = 64 * 1024
const MaxDelivery = 512 * 1024
const MaxJournal = 512 * 1024
const MaxRecoveryRecord = 2 * 1024 * 1024
const ChunkBytes = 32 * 1024
const GrantMilliseconds = 60000

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Scope struct {
	HostID     string `json:"host_id"`
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
}
type Intent struct {
	ID              string                  `json:"id"`
	Scope           Scope                   `json:"scope"`
	Action          string                  `json:"action"`
	ProviderVersion int                     `json:"provider_version"`
	ExpiresAt       string                  `json:"expires_at"`
	Binding         *actioncontract.Binding `json:"binding,omitempty"`
	InputJSON       string                  `json:"input_json,omitempty"`
	SettingsJSON    string                  `json:"settings_json,omitempty"`
	Archive         *actioncontract.Archive `json:"archive,omitempty"`
}

func (i Intent) Fence() hostmutation.Fence {
	return hostmutation.Fence{Version: 1, JobID: i.ID, HostID: i.Scope.HostID, AgentID: i.Scope.AgentID, Generation: i.Scope.Generation}
}
func (i Intent) Validate() error {
	expiry, e := time.Parse(time.RFC3339Nano, i.ExpiresAt)
	if !i.Fence().Valid() || i.ProviderVersion < 1 || e != nil || expiry.IsZero() || !regexp.MustCompile(`^[a-z][a-z0-9_.]{1,63}$`).MatchString(i.Action) {
		return errors.New("maintenance_scope_invalid")
	}
	if i.Binding == nil && (i.InputJSON != "" || i.SettingsJSON != "" || i.Archive != nil) {
		return errors.New("action_binding_invalid")
	}
	if i.Binding != nil && (i.Binding.Validate() != nil || i.Binding.ActionID != i.Action || i.Binding.ActionVersion != i.ProviderVersion || len(i.InputJSON) > MaxControl || len(i.SettingsJSON) > MaxControl) {
		return errors.New("action_binding_invalid")
	}
	if i.Binding != nil {
		if i.Archive == nil || !i.Archive.Valid() {
			return errors.New("action_binding_invalid")
		}
		for _, value := range []struct{ Raw, SHA string }{{i.InputJSON, i.Binding.InputSHA}, {i.SettingsJSON, i.Binding.SettingsSHA}} {
			sum := sha256.Sum256([]byte(value.Raw))
			if !json.Valid([]byte(value.Raw)) || hex.EncodeToString(sum[:]) != value.SHA {
				return errors.New("action_binding_invalid")
			}
		}
	}
	return nil
}

type Start struct {
	JobID      string `json:"job_id"`
	AttemptID  string `json:"attempt_id"`
	BootID     string `json:"boot_id"`
	NotAfterMS int64  `json:"not_after_ms"`
}
type Result struct {
	Effect         string                        `json:"effect"`
	Reason         *string                       `json:"reason"`
	LocalVerified  bool                          `json:"local_verified"`
	ProcessRunning bool                          `json:"process_running"`
	Facts          json.RawMessage               `json:"facts"`
	ReasonMessage  *actioncontract.PackageReason `json:"reason_message,omitempty"`
}
type Prepared struct {
	Plan      json.RawMessage
	Digest    string
	NoChanges bool
	Effect    string
	Result    *Result
}

// Providers cannot issue authority, reset an attempt, replace scope or clear fences.
type Provider interface {
	Prepare(context.Context) (Prepared, error)
	Fetch(context.Context, string) error
	Apply(string, Start) (Result, error)
	Recover(string, Start) (Result, error)
}
type Definition struct {
	ID            string
	Version       int
	Approval      bool
	New           func() Provider
	ErrorFacts    json.RawMessage
	PrepareEffect string
	Resolve       func(Intent) (Definition, error)
}
type Registry []Definition

func (r Registry) Resolve(id string, version int) (Definition, error) {
	for _, d := range r {
		if d.ID == id && d.Version == version && d.New != nil {
			return d, nil
		}
	}
	return Definition{}, errors.New("maintenance_unsupported")
}

// ResolveIntent permits package loading only with the entire immutable identity.
// Production uses one generic resolver; compiled registries remain legacy fixtures.
func (r Registry) ResolveIntent(intent Intent) (Definition, error) {
	if err := intent.Validate(); err != nil {
		return Definition{}, err
	}
	if intent.Binding == nil {
		return r.Resolve(intent.Action, intent.ProviderVersion)
	}
	for _, definition := range r {
		if definition.Resolve != nil {
			resolved, err := definition.Resolve(intent)
			if err != nil || resolved.ID != intent.Action || resolved.Version != intent.ProviderVersion || resolved.New == nil {
				return Definition{}, errors.New("action_definition_invalid")
			}
			return resolved, nil
		}
	}
	return Definition{}, errors.New("maintenance_unsupported")
}
