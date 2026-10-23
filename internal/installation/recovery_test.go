package installation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

func TestPolicyDecisionAndRecheck(t *testing.T) {
	m := agentrelease.Manifest{Version: "0.0.9", SourceMin: "0.0.8", SourceMax: "0.0.9", Automatic: true, Capabilities: []string{agentrelease.Capability}}
	c := &agentrelease.Candidate{Manifest: "exact signed bytes", Signature: "signature"}
	p := agentrelease.Policy{Allowed: true, Candidate: c, Capabilities: []string{agentrelease.Capability}}
	for _, tc := range []struct {
		name                                string
		local, allowed, eligible, automatic bool
		want                                string
	}{
		{"eligible", true, true, true, true, "available"}, {"local prohibition", false, true, true, true, "disabled"},
		{"app prohibition", true, false, true, true, "disabled"}, {"manual-only release", true, true, false, true, "disabled"},
		{"explicit root request", false, false, false, false, "available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testManifest, testPolicy := m, p
			testManifest.Automatic = tc.eligible
			testPolicy.Allowed = tc.allowed
			got, e := updateDecision(testManifest, "0.0.8", testPolicy, tc.local, tc.automatic)
			if e != nil || got != tc.want {
				t.Fatalf("%s %v", got, e)
			}
		})
	}
	if got, _ := updateDecision(m, "0.0.9", p, true, true); got != "idle" {
		t.Fatal("no same-version installation")
	}
	if got, _ := updateDecision(m, "0.0.7", p, true, false); got != "manual_required" {
		t.Fatal("manual requests must preserve source bounds")
	}
	fresh := p
	fresh.Allowed = false
	if recheck(p, fresh, true, true, nil) == nil {
		t.Fatal("app opt-out after download ignored")
	}
	if recheck(p, p, false, true, nil) == nil {
		t.Fatal("local opt-out ignored")
	}
	fresh = p
	fresh.Candidate = &agentrelease.Candidate{Manifest: c.Manifest + " ", Signature: c.Signature}
	if recheck(p, fresh, true, false, nil) == nil {
		t.Fatal("target changed after download")
	}
	p.Capabilities = nil
	if got, _ := updateDecision(m, "0.0.8", p, true, false); got != "app_required" {
		t.Fatal("app capability required")
	}
}

func TestForwardRepairNeverDowngrades(t *testing.T) {
	d := t.TempDir()
	if e := os.Symlink("releases/0.0.10", filepath.Join(d, "current")); e != nil {
		t.Fatal(e)
	}
	if repairVersion(d, "0.0.9") == nil {
		t.Fatal("repair could downgrade")
	}
	if repairVersion(d, "0.0.10") != nil {
		t.Fatal("forward completion rejected")
	}
}

func TestRootLockAndRecoveryJournal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("run this disposable fixture as root; it changes only its temporary directory")
	}
	d := t.TempDir()
	if e := os.MkdirAll(filepath.Join(d, "updater", "private"), 0700); e != nil {
		t.Fatal(e)
	}
	g, e := lock(d)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	if another, e := lock(d); e == nil {
		another.Close()
		t.Fatal("overlapping updater acquired lock")
	}
	for _, phase := range []string{"staged", "switching", "awaiting_confirmation", "repair_attempted"} {
		if e = record(d, transaction{Target: "0.0.9", Previous: "0.0.8", Phase: phase}); e != nil {
			t.Fatal(e)
		}
		pending, e := pendingTransaction(d)
		if e != nil || pending == nil || pending.Phase != phase {
			t.Fatal("interrupted state lost", e)
		}
	}
	if e = record(d, transaction{Target: "0.0.9", Previous: "0.0.8", Phase: "completed"}); e != nil {
		t.Fatal(e)
	}
	if p, e := pendingTransaction(d); e != nil || p != nil {
		t.Fatal("completed journal blocks next update")
	}
}

func TestCopyPreservesQueuesAndRejectsLinks(t *testing.T) {
	d := t.TempDir()
	source, target := filepath.Join(d, "old"), filepath.Join(d, "new")
	os.Mkdir(source, 0700)
	os.Mkdir(target, 0700)
	state := []byte(`{"sequence":12,"pending":{"exact":"replay"}}`)
	os.WriteFile(filepath.Join(source, "state.json"), state, 0600)
	if e := copyTree(source, target, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(target, "state.json"))
	if e != nil || string(got) != string(state) {
		t.Fatal("persisted state changed")
	}
	os.Symlink(filepath.Join(source, "state.json"), filepath.Join(source, "link"))
	os.Mkdir(filepath.Join(d, "reject"), 0700)
	if copyTree(source, filepath.Join(d, "reject"), os.Geteuid(), os.Getegid()) == nil {
		t.Fatal("legacy symlink followed")
	}
}

func TestPublicDirectoriesIgnoreCallerUmask(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("disposable root path fixture")
	}
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	path := filepath.Join(t.TempDir(), "public", "nested")
	if e := rootDirectory(path, 0755); e != nil {
		t.Fatal(e)
	}
	i, _ := os.Stat(path)
	if i.Mode().Perm() != 0755 {
		t.Fatal("monitoring cannot traverse release paths")
	}
}

func TestSignedUnitTemplatesParseBeforeActivation(t *testing.T) {
	base := t.TempDir() + "/custom base"
	dir := t.TempDir()
	for _, name := range []string{Service, UpdateService, Timer} {
		b, e := os.ReadFile(filepath.Join("../../deploy/systemd/managed", name))
		if e != nil {
			t.Fatal(e)
		}
		text := strings.ReplaceAll(string(b), "@BASE@", unitQuote(base))
		text = strings.ReplaceAll(text, unitQuote(base+"/current/tinywarden-agent"), "/usr/bin/true")
		if e = os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	c := exec.Command("/usr/bin/systemd-analyze", "verify", filepath.Join(dir, Service), filepath.Join(dir, UpdateService), filepath.Join(dir, Timer))
	if output, e := c.CombinedOutput(); e != nil {
		t.Fatalf("service parsing failed: %v %s", e, output)
	}
}

func TestUpdateStatusRetainsCheckGuardAndLastSuccess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("disposable root-owned status fixture")
	}
	d := t.TempDir()
	if e := os.Mkdir(filepath.Join(d, "updater"), 0755); e != nil {
		t.Fatal(e)
	}
	checked := time.Now().UTC().Format(time.RFC3339Nano)
	updated := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	for _, next := range []agentrelease.UpdateStatus{
		{Outcome: "installing", CheckedAt: checked, Target: "0.0.10"},
		{Outcome: "awaiting_confirmation", Target: "0.0.10"},
		{Outcome: "updated", UpdatedAt: updated, Target: "0.0.10"},
		{Outcome: "disabled"},
		{Outcome: "idle"},
	} {
		if e := status(d, next); e != nil {
			t.Fatal(e)
		}
	}
	b, e := agentrelease.ReadPublic(filepath.Join(d, "updater", "status.json"), 2048)
	var got agentrelease.UpdateStatus
	if e != nil || agentrelease.Decode(b, &got) != nil || got.CheckedAt != checked || got.UpdatedAt != updated || got.Outcome != "idle" || got.Target != "" || got.Error != "" {
		t.Fatalf("last check/success lost or obsolete outcome fields retained: %+v, %v", got, e)
	}
	at, e := time.Parse(time.RFC3339Nano, got.CheckedAt)
	if e != nil || time.Since(at) >= 20*time.Hour {
		t.Fatal("successful update reset the automatic check guard")
	}
	later := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if e = status(d, agentrelease.UpdateStatus{Outcome: "failed", CheckedAt: later, Error: "app_unreachable"}); e != nil {
		t.Fatal(e)
	}
	b, e = agentrelease.ReadPublic(filepath.Join(d, "updater", "status.json"), 2048)
	if e != nil || agentrelease.Decode(b, &got) != nil || got.CheckedAt != later || got.UpdatedAt != updated || got.Error != "app_unreachable" {
		t.Fatal("later failed check erased the last successful update")
	}
}
