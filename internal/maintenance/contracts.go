// Package maintenance coordinates typed root actions independently of monitoring.
package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

const MaxEvents = 256
const Capability = "maintenance_jobs.v1"
const MaxPlan = 4 * 1024 * 1024
const MaxControl = 64 * 1024
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
	ID              string `json:"id"`
	Scope           Scope  `json:"scope"`
	Action          string `json:"action"`
	ProviderVersion int    `json:"provider_version"`
	ExpiresAt       string `json:"expires_at"`
}

func (i Intent) Fence() hostmutation.Fence {
	return hostmutation.Fence{Version: 1, JobID: i.ID, HostID: i.Scope.HostID, AgentID: i.Scope.AgentID, Generation: i.Scope.Generation}
}
func (i Intent) Validate() error {
	expiry, e := time.Parse(time.RFC3339Nano, i.ExpiresAt)
	if !i.Fence().Valid() || i.ProviderVersion < 1 || e != nil || expiry.IsZero() || !regexp.MustCompile(`^[a-z][a-z0-9_.]{1,63}$`).MatchString(i.Action) {
		return errors.New("maintenance_scope_invalid")
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
	Effect         string          `json:"effect"`
	Reason         *string         `json:"reason"`
	LocalVerified  bool            `json:"local_verified"`
	ProcessRunning bool            `json:"process_running"`
	Facts          json.RawMessage `json:"facts"`
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
	ID         string
	Version    int
	Approval   bool
	New        func() Provider
	ErrorFacts json.RawMessage
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
