package release

import (
	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"path/filepath"
	"time"
)

type MaintenanceReadiness struct {
	Capability  string                       `json:"capability"`
	WorkerReady bool                         `json:"worker_ready"`
	Actions     []MaintenanceActionReadiness `json:"actions"`
}
type MaintenanceActionReadiness struct {
	ID      string   `json:"id"`
	Ready   bool     `json:"ready"`
	Missing []string `json:"missing"`
}
type MaintenanceStatus struct {
	Schema          int                               `json:"schema_version"`
	BinarySHA       string                            `json:"binary_sha256"`
	CheckedAt       string                            `json:"checked_at"`
	Report          MaintenanceReadiness              `json:"report"`
	Probes          []actioncontract.Probe            `json:"probes,omitempty"`
	ProbeCursor     *string                           `json:"probe_cursor,omitempty"`
	ActionReadiness []actioncontract.Readiness        `json:"action_readiness,omitempty"`
	Origins         []actioncontract.OriginPermission `json:"origins,omitempty"`
}

func maintenanceReadiness(base, binary string) *MaintenanceReadiness {
	raw, e := ReadPublic(filepath.Join(base, "updater", "maintenance.json"), 512*1024)
	if e != nil {
		return nil
	}
	var status MaintenanceStatus
	if Decode(raw, &status) != nil || status.Schema != 1 || status.BinarySHA != binary || status.Report.Capability != "maintenance_jobs.v1" && status.Report.Capability != actioncontract.Capability || len(status.Report.Actions) > 8 || len(status.Probes) > 32 || len(status.ActionReadiness) > 32 || len(status.Origins) > 64 {
		return nil
	}
	at, e := time.Parse(time.RFC3339Nano, status.CheckedAt)
	now := time.Now()
	if e != nil || at.After(now) || now.Sub(at) > 3*time.Minute {
		return nil
	}
	return &status.Report
}
