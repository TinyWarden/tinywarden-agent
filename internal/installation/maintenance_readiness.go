package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
	"github.com/TinyWarden/tinywarden-agent/internal/skillactions"
	"github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
)

type maintenanceProbeUpdate struct {
	Probes []actioncontract.Probe
	Cursor *string
}

func actionPolicyPath(base string) string {
	return filepath.Join(base, "updater", "private", "action-policy.json")
}

func maintenanceRuntimeAssets(base string) string {
	// Managed config intentionally omits a runtime override. Root workers bind the
	// same signed current platform used by the daemon and checked by Verify.
	return filepath.Join(base, "current", "runtime")
}

func readMaintenanceStatus(base string) (agentrelease.MaintenanceStatus, error) {
	raw, err := agentrelease.ReadPublic(filepath.Join(base, "updater", "maintenance.json"), maintenance.MaxDelivery)
	if err != nil {
		return agentrelease.MaintenanceStatus{}, err
	}
	var status agentrelease.MaintenanceStatus
	if controlplane.DecodeArtifact(raw, &status, 180000) != nil || len(status.Probes) > 32 || len(status.ActionReadiness) > 32 || len(status.Origins) > 64 {
		return status, errors.New("maintenance_readiness_invalid")
	}
	return status, nil
}

func maintenanceStatus(ctx context.Context, base string, m agentrelease.Manifest, updates ...maintenanceProbeUpdate) error {
	status, _ := readMaintenanceStatus(base)
	if status.BinarySHA != m.BinarySHA256 {
		status = agentrelease.MaintenanceStatus{}
	}
	if len(updates) > 0 {
		status.Probes = updates[0].Probes
		status.ProbeCursor = updates[0].Cursor
	}
	report := agentrelease.MaintenanceReadiness{Capability: actioncontract.Capability, WorkerReady: true, Actions: []agentrelease.MaintenanceActionReadiness{}}
	for _, name := range maintenanceUnits {
		if ownedRegistration(base, name) != nil {
			report.WorkerReady = false
			continue
		}
		template, err := os.ReadFile(filepath.Join(base, "current", "systemd", name))
		if err != nil {
			report.WorkerReady = false
			continue
		}
		installed, err := agentrelease.ReadPublic(filepath.Join(base, "updater", "systemd", name), 16384)
		if err != nil || !bytes.Equal(installed, []byte(strings.ReplaceAll(string(template), "@BASE@", unitQuote(base)))) {
			report.WorkerReady = false
		}
	}
	if system(ctx, "is-enabled", "--quiet", MaintenanceTimer) != nil || system(ctx, "is-active", "--quiet", MaintenanceTimer) != nil {
		report.WorkerReady = false
	}
	if pending, err := pendingTransaction(base); err != nil || pending != nil {
		report.WorkerReady = false
	}
	assets := maintenanceRuntimeAssets(base)
	_, err := config(base)
	if err != nil || !(skillruntime.Client{Assets: assets}).SupportsSDK3() {
		report.WorkerReady = false
	}
	status.Origins = []actioncontract.OriginPermission{}
	status.ActionReadiness = []actioncontract.Readiness{}
	if err == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		raw, _ := json.Marshal(struct {
			Probes []actioncontract.Probe `json:"probes"`
		}{status.Probes})
		if status.Probes == nil {
			raw = []byte(`{"probes":[]}`)
		}
		cmd := exec.CommandContext(probeCtx, "/usr/bin/python3", "-I", "-B", filepath.Join(assets, "actions", "readiness.py"), actionPolicyPath(base))
		cmd.Stdin = bytes.NewReader(raw)
		cmd.Dir = "/"
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC"}
		out, err := cmd.Output()
		cancel()
		var value struct {
			Origins []actioncontract.OriginPermission `json:"origins"`
			Actions []actioncontract.Readiness        `json:"actions"`
		}
		if err != nil || len(out) > maintenance.MaxControl || controlplane.Decode(out, &value) != nil {
			report.WorkerReady = false
		} else {
			status.Origins = value.Origins
			status.ActionReadiness = value.Actions
		}
	}
	status.Schema = 1
	status.BinarySHA = m.BinarySHA256
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	status.Report = report
	return atomicFile(filepath.Join(base, "updater", "maintenance.json"), status, 0644)
}

func maintenanceRegistry(base string, guard *os.File, verify func() error, client *controlplane.Client, credential string) maintenance.Registry {
	loader := skillactions.Loader{Base: base, Policy: actionPolicyPath(base), Runtime: skillruntime.Client{Assets: maintenanceRuntimeAssets(base)}, Guard: guard, Verify: verify,
		Download: func(ctx context.Context, intent maintenance.Intent, store string) (string, error) {
			return client.FetchArtifact(ctx, credential, "/api/v1/agent/maintenance-jobs/"+intent.ID+"/package", intent.Binding.ContentSHA, store, *intent.Archive)
		}, Log: actionLogUploader(client, credential)}
	return loader.Registry()
}

func actionLogUploader(client *controlplane.Client, credential string) func(context.Context, maintenance.Intent, []json.RawMessage) error {
	return func(ctx context.Context, intent maintenance.Intent, entries []json.RawMessage) error {
		const requestLimit, responseLimit = 160 * 1024, 16 * 1024
		input := map[string]any{"schema_version": 1, "job_id": intent.ID, "scope": intent.Scope, "binding": intent.Binding}
		for len(entries) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			count := len(entries)
			for {
				input["entries"] = entries[:count]
				wire, err := json.Marshal(input)
				if err != nil {
					return err
				}
				// Bound the actual encoding, including HTML escaping and job scope.
				if len(wire) <= requestLimit {
					break
				}
				if count == 1 {
					return errors.New("maintenance_log_body_invalid")
				}
				count /= 2
			}
			// Two limits opt into the transport's strict response validator.
			_, err := client.Post(ctx, "/api/v2/agent/action-logs", credential, input, false,
				actionLogReceipt(entries[:count]), requestLimit, responseLimit)
			if err != nil {
				return err
			}
			entries = entries[count:]
		}
		return nil
	}
}

func actionLogReceipt(entries []json.RawMessage) func([]byte) error {
	return func(raw []byte) error {
		var reply struct {
			Schema   int   `json:"schema_version"`
			Accepted []int `json:"accepted"`
		}
		if controlplane.Decode(raw, &reply) != nil || reply.Schema != 1 || len(reply.Accepted) != len(entries) {
			return errors.New("maintenance_log_receipt_invalid")
		}
		for index, entry := range entries {
			var value struct {
				Sequence int `json:"sequence"`
			}
			if json.Unmarshal(entry, &value) != nil || reply.Accepted[index] != value.Sequence {
				return errors.New("maintenance_log_receipt_invalid")
			}
		}
		return nil
	}
}
