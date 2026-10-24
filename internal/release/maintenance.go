package release

import (
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
	Schema    int                  `json:"schema_version"`
	BinarySHA string               `json:"binary_sha256"`
	CheckedAt string               `json:"checked_at"`
	Report    MaintenanceReadiness `json:"report"`
}

func maintenanceReadiness(base, binary string) *MaintenanceReadiness {
	raw, e := ReadPublic(filepath.Join(base, "updater", "maintenance.json"), 8192)
	if e != nil {
		return nil
	}
	var status MaintenanceStatus
	if Decode(raw, &status) != nil || status.Schema != 1 || status.BinarySHA != binary || status.Report.Capability != "maintenance_jobs.v1" || len(status.Report.Actions) > 8 {
		return nil
	}
	at, e := time.Parse(time.RFC3339Nano, status.CheckedAt)
	now := time.Now()
	if e != nil || at.After(now) || now.Sub(at) > 3*time.Minute {
		return nil
	}
	return &status.Report
}
