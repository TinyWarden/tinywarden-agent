package skillactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	"github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
)

const nativeSource = `from pathlib import Path
import fcntl,json
COUNTER = Path(COUNTER_PATH)
LOCK = Path(LOCK_PATH)
def prepare(request,context):
    return {'plan_json':'{"complete":true}','has_changes':True,'effect':'none','result':None}
def fetch(request,context):
    return {'downloaded':True,'plan_sha256':context.plan_sha256}
def apply(request,context):
    context.check_start()
    count=int(COUNTER.read_text())+1 if COUNTER.exists() else 1
    COUNTER.write_text(str(count))
    return {'effect':'changed','reason':None,'local_verified':True,'process_running':False,
            'facts':{'complete':True},'receipt_json':json.dumps({'count':count})}
def verify(request,context):
    return {'verified':int(COUNTER.read_text())==request['receipt']['count']}
def inspect(request,context):
    with LOCK.open('a+b') as stream:
        fcntl.flock(stream,fcntl.LOCK_EX)
        if COUNTER.read_text()!='1': raise RuntimeError('fixture state differs')
        context.commit({'complete':True})
`

func nativeCommand(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("/usr/bin/python3", args...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled author fixture failed: %v %s", err, raw)
	}
	return raw
}

func TestDeliveredActionNativeLifecycle(t *testing.T) {
	if os.Getenv("TW_SKILL_ACTION_NATIVE") != "1" {
		t.Skip("explicit disposable-host native scope required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native scope requires root")
	}
	kit := os.Getenv("TW_SKILL_ACTION_SDK")
	if !filepath.IsAbs(kit) {
		t.Fatal("absolute standalone SDK kit required")
	}
	base, err := os.MkdirTemp("/var/lib", "tw-action-go-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	if err = os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := hostmutation.Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	source := filepath.Join(base, "authored")
	tool := filepath.Join(kit, "sdk", "python", "skill_tool.py")
	nativeCommand(t, "-I", "-S", "-B", tool, "init", source, "--id", "native-proof/counter", "--publisher", "Native fixture", "--with-actions")
	path := filepath.Join(source, "skill.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]json.RawMessage
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("fixture manifest")
	}
	var actions []map[string]any
	if json.Unmarshal(manifest["actions"], &actions) != nil {
		t.Fatal("fixture actions")
	}
	actions[0]["execution_class"] = "host_admin"
	manifest["actions"], _ = json.Marshal(actions)
	raw, _ = json.Marshal(manifest)
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("fixture manifest write")
	}
	entry := "COUNTER_PATH=" + strconv.Quote(filepath.Join(base, "counter")) + "\nLOCK_PATH=" + strconv.Quote(filepath.Join(base, "native.lock")) + "\n" + nativeSource
	if err = os.WriteFile(filepath.Join(source, "actions", "entry.py"), []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(base, "authored.zip")
	raw = nativeCommand(t, "-I", "-S", "-B", tool, "pack", source, "--output", archivePath)
	var packed struct {
		ContentSHA   string `json:"content_sha256"`
		ArchiveSHA   string `json:"archive_sha256"`
		ArchiveBytes int64  `json:"archive_bytes"`
		Actions      []struct {
			ID  string `json:"id"`
			SHA string `json:"descriptor_sha256"`
		} `json:"actions"`
	}
	if json.Unmarshal(raw, &packed) != nil || len(packed.Actions) != 1 {
		t.Fatal("fixture packing")
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer native-fixture-only" || r.URL.Path != "/api/v1/agent/maintenance-jobs/00000004-1111-4111-8111-111111111111/package" {
			t.Error("artifact authority/scope changed")
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("X-TinyWarden-Archive-SHA256", packed.ArchiveSHA)
		w.Header().Set("X-TinyWarden-Content-SHA256", packed.ContentSHA)
		w.Write(archive)
	}))
	defer server.Close()
	client := controlplane.NewClient(server.URL)
	settings := string(manifest["defaults"])
	input := `{"complete":true}`
	binding := &actioncontract.Binding{InstallationID: "00000001-1111-4111-8111-111111111111", SkillID: "native-proof/counter", ContentSHA: packed.ContentSHA,
		ActionID: packed.Actions[0].ID, ActionVersion: 1, DescriptorSHA: packed.Actions[0].SHA, PermissionRevision: "1", SettingsRevision: "1", SettingsSHA: digest([]byte(settings)), InputSHA: digest([]byte(input)), Origin: actioncontract.Origin{GitHubID: "12345", OwnerID: "67890"}}
	intent := maintenance.Intent{ID: "00000004-1111-4111-8111-111111111111", Scope: maintenance.Scope{HostID: "00000002-1111-4111-8111-111111111111", AgentID: "00000003-1111-4111-8111-111111111111", Generation: 1},
		Action: binding.ActionID, ProviderVersion: 1, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Binding: binding, InputJSON: input, SettingsJSON: settings, Archive: &actioncontract.Archive{SHA256: packed.ArchiveSHA, Size: packed.ArchiveBytes}}
	policy := filepath.Join(base, "policy.json")
	if err = os.WriteFile(policy, []byte(`{"schema_version":1,"origins":[{"github_id":"12345","owner_id":"67890","classes":["host_admin"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	loader := Loader{Base: base, Policy: policy, Runtime: skillruntime.Client{Assets: filepath.Join(kit, "sdk", "python", "runtime")}, Guard: guard, Verify: func() error { return nil }, Download: func(ctx context.Context, i maintenance.Intent, store string) (string, error) {
		return client.FetchArtifact(ctx, "native-fixture-only", "/api/v1/agent/maintenance-jobs/"+i.ID+"/package", i.Binding.ContentSHA, store, *i.Archive)
	}}
	definition, err := loader.Registry().ResolveIntent(intent)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := maintenance.Begin(base, intent)
	if err != nil {
		t.Fatal(err)
	}
	journal.Phase = "preparing"
	if err = maintenance.Save(base, *journal); err != nil {
		t.Fatal(err)
	}
	provider := definition.New()
	prepared, err := provider.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = maintenance.SavePlan(base, journal, prepared.Plan, prepared.Digest); err != nil {
		t.Fatal(err)
	}
	if err = provider.Fetch(context.Background(), prepared.Digest); err != nil {
		t.Fatal(err)
	}
	plan, approval := "00000005-1111-4111-8111-111111111111", "00000006-1111-4111-8111-111111111111"
	journal.PlanID = &plan
	journal.ApprovalID = &approval
	journal.Phase = "downloading"
	if err = maintenance.Save(base, *journal); err != nil {
		t.Fatal(err)
	}
	boot, now, err := maintenance.BootClock()
	if err != nil {
		t.Fatal(err)
	}
	start := maintenance.Start{JobID: intent.ID, AttemptID: "00000007-1111-4111-8111-111111111111", BootID: boot, NotAfterMS: now + 50000}
	if err = maintenance.StartApply(base, journal, start); err != nil {
		t.Fatal(err)
	}
	result, err := provider.Apply(prepared.Digest, start)
	if err != nil || !result.LocalVerified {
		t.Fatal("apply", err)
	}
	if _, err = provider.Apply(prepared.Digest, start); err != nil {
		t.Fatal("repeat did not recover exact receipt", err)
	}
	count, err := os.ReadFile(filepath.Join(base, "counter"))
	if err != nil || string(count) != "1" {
		t.Fatal("apply repeated", err, string(count))
	}
	// A fresh provider recovers from the existing regular receipt, never Apply.
	definition, err = loader.Registry().ResolveIntent(intent)
	if err != nil {
		t.Fatal(err)
	}
	provider = definition.New()
	if result, err = provider.Recover(prepared.Digest, start); err != nil || !result.LocalVerified {
		t.Fatal("restart receipt recovery", err)
	}
	inspector, ok := provider.(maintenance.ActionInspector)
	if !ok {
		t.Fatal("generic inspector missing")
	}
	if err = inspector.InspectAction(context.Background(), func(facts json.RawMessage) error {
		if string(facts) != `{"complete":true}` {
			t.Fatal("typed recovery fact lost", string(facts))
		}
		file, e := os.OpenFile(filepath.Join(base, "native.lock"), os.O_RDWR, 0600)
		if e != nil {
			t.Fatal(e)
		}
		defer file.Close()
		if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e == nil {
			t.Fatal("native inspector released lock before callback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	count, _ = os.ReadFile(filepath.Join(base, "counter"))
	if string(count) != "1" {
		t.Fatal("inspection repeated execution")
	}
	// Modified pinned code cannot be substituted when replaying the old receipt.
	if err = os.WriteFile(filepath.Join(base, "updater", "private", "action-packages", binding.ContentSHA, "actions", "entry.py"), []byte("raise RuntimeError('tampered')\n"), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Recover(prepared.Digest, start); err == nil {
		t.Fatal("tampered package resumed")
	}
}
