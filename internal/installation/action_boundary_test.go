package installation

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
	"github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
)

func TestProductionAgentHasNoEmbeddedDomainExecutor(t *testing.T) {
	root := filepath.Clean("../..")
	cmd := exec.Command("go", "list", "-deps", "./cmd/tinywarden-agent")
	cmd.Dir = root
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(string(raw), e)
	}
	if strings.Contains(string(raw), "/maintenance/apt") {
		t.Fatal("retired APT provider linked into production agent")
	}
	if _, e = os.Stat(filepath.Join(root, "internal", "maintenance", "apt")); !os.IsNotExist(e) {
		t.Fatal("retired executor retained in active source")
	}
	for _, name := range []string{"actions/runner.py", "actions/guest.py", "actions/guardian.py", "actions/probe.py", "actions/readiness.py"} {
		raw, e = os.ReadFile(filepath.Join(root, "sdk", "python", "runtime", name))
		if e != nil {
			t.Fatal(e)
		}
		for _, forbidden := range []string{"import apt", "apt_pkg", "packages.upgrade_conservative", "packages.refresh.v1"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("domain executor in generic runtime %s", name)
			}
		}
	}
}

func TestManagedMaintenanceRuntimeWithoutConfigOverride(t *testing.T) {
	base := t.TempDir()
	platform := filepath.Join(base, "versions", "candidate")
	source, err := filepath.Abs("../../sdk/python/runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(filepath.Join(platform, "runtime"), os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(platform, filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}
	assets := maintenanceRuntimeAssets(base)
	if !(skillruntime.Client{Assets: assets}).SupportsSDK3() {
		t.Fatal("managed worker cannot use its signed-platform runtime layout")
	}
	// Resolving the installed path must retain manifest verification. This unit
	// fixture has no root-owned publisher policy; signed root readiness is native.
	if err = os.WriteFile(filepath.Join(platform, "runtime", "skill_actions.py"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if (skillruntime.Client{Assets: assets}).SupportsSDK3() {
		t.Fatal("managed worker accepted a changed runtime file")
	}
}

func TestMaintenancePollArraysSurvivePersistedStatus(t *testing.T) {
	cursor := "00000001-1111-4111-8111-111111111111"
	for _, status := range []agentrelease.MaintenanceStatus{
		{},
		{Report: agentrelease.MaintenanceReadiness{WorkerReady: true}, Origins: []actioncontract.OriginPermission{}, ActionReadiness: []actioncontract.Readiness{}},
		{ProbeCursor: &cursor, Report: agentrelease.MaintenanceReadiness{WorkerReady: true},
			Origins: []actioncontract.OriginPermission{{GitHubID: "123", OwnerID: "456", Classes: []string{"host_admin"}}},
			ActionReadiness: []actioncontract.Readiness{{GrantID: cursor, InstallationID: cursor, ContentSHA: strings.Repeat("a", 64),
				DescriptorSHA: strings.Repeat("b", 64), ID: "example.action.v1", Revision: "1", Ready: true, Missing: []string{}}}},
	} {
		encoded, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		var loaded agentrelease.MaintenanceStatus
		if err = json.Unmarshal(encoded, &loaded); err != nil {
			t.Fatal(err)
		}
		raw := maintenancePollData(loaded)
		var wire map[string]json.RawMessage
		if json.Unmarshal(raw, &wire) != nil || len(wire) != 4 || !bytes.HasPrefix(wire["origins"], []byte("[")) || !bytes.HasPrefix(wire["readiness"], []byte("[")) {
			t.Fatal("idle poll lost its array contract", string(raw))
		}
		var roundTrip struct {
			Cursor      *string                           `json:"probe_cursor"`
			WorkerReady bool                              `json:"worker_ready"`
			Origins     []actioncontract.OriginPermission `json:"origins"`
			Readiness   []actioncontract.Readiness        `json:"readiness"`
		}
		if json.Unmarshal(raw, &roundTrip) != nil || roundTrip.WorkerReady != status.Report.WorkerReady || !reflect.DeepEqual(roundTrip.Cursor, status.ProbeCursor) || len(roundTrip.Origins) != len(status.Origins) || len(roundTrip.Readiness) != len(status.ActionReadiness) {
			t.Fatal("idle poll changed readiness or authority", string(raw))
		}
		if len(status.Origins) > 0 && (!reflect.DeepEqual(roundTrip.Origins, status.Origins) || !reflect.DeepEqual(roundTrip.Readiness, status.ActionReadiness)) {
			t.Fatal("idle poll changed nonempty reports", string(raw))
		}
	}
}
