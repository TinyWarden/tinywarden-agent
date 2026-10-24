package apt

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

// Native tests require an explicit disposable fixture with signed HTTP sources
// mounted privately at /etc/apt. They must never select the serving host by default.
func nativeFixture(t *testing.T) string {
	t.Helper()
	root := os.Getenv("TW_MA1_NATIVE_PROVIDER")
	if root == "" {
		t.Skip("requires authorized disposable Debian package fixture")
	}
	if os.Geteuid() != 0 || !filepath.IsAbs(root) {
		t.Fatal("invalid disposable fixture")
	}
	if raw, err := os.ReadFile(filepath.Join(root, "disposable-authorized")); err != nil || string(raw) != "MA1 native package tests\n" {
		t.Fatal("missing disposable authorization marker")
	}
	return root
}

func prepareNative(t *testing.T, base string) (*Provider, *maintenance.Journal) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := hostmutation.Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guard.Close() })
	intent := maintenance.Intent{ID: "11111111-1111-4111-8111-111111111111", Scope: maintenance.Scope{
		HostID: "22222222-2222-4222-8222-222222222222", AgentID: "33333333-3333-4333-8333-333333333333", Generation: 1},
		Action: "packages.upgrade_conservative.v1", ProviderVersion: 1, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	j, err := maintenance.Begin(base, intent)
	if err != nil {
		t.Fatal(err)
	}
	j.Phase = "preparing"
	if err = maintenance.Save(base, *j); err != nil {
		t.Fatal(err)
	}
	p := &Provider{Base: base, Action: intent.Action, Guard: guard}
	prepared, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatal("native preparation", err)
	}
	if err = maintenance.SavePlan(base, j, prepared.Plan, prepared.Digest); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Packages []struct {
			Name string `json:"name"`
		} `json:"packages"`
	}
	if json.Unmarshal(prepared.Plan, &plan) != nil || len(plan.Packages) != 1 {
		t.Fatal("fixture must expose exactly one reviewed upgrade")
	}
	for _, item := range plan.Packages {
		if item.Name != os.Getenv("TW_MA1_PACKAGE") {
			t.Fatal("unexpected transaction", item.Name)
		}
	}
	approval, planID := "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
	j.ApprovalID, j.PlanID, j.Phase = &approval, &planID, "downloading"
	if err = maintenance.Save(base, *j); err != nil {
		t.Fatal(err)
	}
	if err = p.Fetch(context.Background(), prepared.Digest); err != nil {
		t.Fatal("native download", err)
	}
	boot, now, err := maintenance.BootClock()
	if err != nil {
		t.Fatal(err)
	}
	start := maintenance.Start{JobID: j.Intent.ID, AttemptID: "66666666-6666-4666-8666-666666666666", BootID: boot, NotAfterMS: now + 60000}
	if err = maintenance.StartApply(base, j, start); err != nil {
		t.Fatal(err)
	}
	return p, j
}

func TestNativeProviderRoundTrip(t *testing.T) {
	root := nativeFixture(t)
	p, j := prepareNative(t, t.TempDir())
	result, err := p.Apply(*j.PlanSHA, *j.Start)
	if err != nil || !result.LocalVerified || result.Effect != "packages_changed" {
		t.Fatal("exact native apply", result, err)
	}
	var facts map[string]any
	if json.Unmarshal(result.Facts, &facts) != nil || facts["configuration_review"] != true || facts["services_restart_required"] == nil {
		t.Fatal("native conffile/service review missing", string(result.Facts))
	}
	for range 2 {
		recovered, err := p.Recover(*j.PlanSHA, *j.Start)
		if err != nil || !recovered.LocalVerified || string(recovered.Facts) != string(result.Facts) {
			t.Fatal("native immutable receipt recovery", recovered, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "attempts")); err != nil || string(raw) != "attempt\n" {
		t.Fatal("native apply replayed", string(raw), err)
	}
}

func TestNativeProviderFailedScriptNeverReplays(t *testing.T) {
	root := nativeFixture(t)
	p, j := prepareNative(t, t.TempDir())
	_, err := p.Apply(*j.PlanSHA, *j.Start)
	var failed providerError
	if !errors.As(err, &failed) || failed.result.LocalVerified || failed.result.Effect != "unknown" {
		t.Fatal("partial native failure lost", err)
	}
	if _, err = p.Recover(*j.PlanSHA, *j.Start); !errors.As(err, &failed) {
		t.Fatal("failed receipt falsely recovered", err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "attempts")); err != nil || string(raw) != "attempt\n" {
		t.Fatal("failed native transaction replayed", string(raw), err)
	}
}

func TestNativeProviderForeignArchitecture(t *testing.T) {
	root := nativeFixture(t)
	p, j := prepareNative(t, t.TempDir())
	plan, err := maintenance.ReadPlan(p.Base)
	if err != nil || !strings.Contains(string(plan), `"architecture":"i386"`) || strings.Contains(string(plan), `"name":"tw-ma1-foreign:i386"`) {
		t.Fatal("foreign architecture was conflated with package name", string(plan), err)
	}
	result, err := p.Apply(*j.PlanSHA, *j.Start)
	if err != nil || !result.LocalVerified {
		t.Fatal("foreign native upgrade", err)
	}
	if _, err = p.Recover(*j.PlanSHA, *j.Start); err != nil {
		t.Fatal("foreign native receipt verification", err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "attempts")); err != nil || string(raw) != "attempt\n" {
		t.Fatal("foreign transaction replayed", string(raw), err)
	}
}

func TestNativeProviderGrantExpiryDoesNotKillInstall(t *testing.T) {
	_ = nativeFixture(t)
	p, j := prepareNative(t, t.TempDir())
	_, now, err := maintenance.BootClock()
	if err != nil {
		t.Fatal(err)
	}
	j.Start.NotAfterMS = now + 5000
	if err = maintenance.Save(p.Base, *j); err != nil {
		t.Fatal(err)
	}
	result, err := p.Apply(*j.PlanSHA, *j.Start)
	if err != nil || !result.LocalVerified {
		t.Fatal("start expiry interrupted an existing native transaction", err)
	}
	_, now, err = maintenance.BootClock()
	if err != nil || now <= j.Start.NotAfterMS {
		t.Fatal("fixture did not cross the start deadline", err)
	}
}

func TestNativeProviderCancelledRefreshCleansChildren(t *testing.T) {
	_ = nativeFixture(t)
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := hostmutation.Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	i := maintenance.Intent{ID: "11111111-1111-4111-8111-111111111111", Scope: maintenance.Scope{
		HostID: "22222222-2222-4222-8222-222222222222", AgentID: "33333333-3333-4333-8333-333333333333", Generation: 1},
		Action: "packages.refresh.v1", ProviderVersion: 1, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	if _, err = maintenance.Begin(base, i); err != nil {
		t.Fatal(err)
	}
	p := &Provider{Base: base, Guard: guard, Action: i.Action}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := p.Prepare(ctx); finished <- err }()
	var readerPID string
	children := func(pid string) []string {
		var children []string
		paths, _ := filepath.Glob("/proc/" + pid + "/task/*/children")
		for _, path := range paths {
			raw, _ := os.ReadFile(path)
			children = append(children, strings.Fields(string(raw))...)
		}
		return children
	}
	// The fixture's HTTP server is deliberately stopped. Wait until apt-get, not
	// merely its Python parent, exists before requesting safe-phase cancellation.
	waitFor(t, func() bool {
		for _, helper := range children(strconv.Itoa(os.Getpid())) {
			for _, child := range children(helper) {
				cmd, _ := os.ReadFile("/proc/" + child + "/cmdline")
				if strings.HasPrefix(string(cmd), "/usr/bin/apt-get\x00update\x00") {
					readerPID = child
					return true
				}
			}
		}
		return false
	})
	cancel()
	select {
	case err = <-finished:
		if err == nil {
			t.Fatal("cancelled stalled refresh succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("safe-phase cancellation did not unwind child cleanup")
	}
	if _, err = os.Stat("/proc/" + readerPID); !os.IsNotExist(err) {
		t.Fatal("refresh child survived cancellation", readerPID, err)
	}
}

func TestNativeProviderCrashOwner(t *testing.T) {
	root := nativeFixture(t)
	if os.Getenv("TW_MA1_CRASH_CHILD") != "1" {
		t.Skip("child process of owner-exit proof only")
	}
	p, j := prepareNative(t, filepath.Join(root, "crashed-owner"))
	_, err := p.Apply(*j.PlanSHA, *j.Start)
	t.Fatal("fixture owner should have been terminated during apply", err)
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("native fixture did not reach its expected boundary")
}

func TestNativeProviderSurvivesOwnerExit(t *testing.T) {
	root := nativeFixture(t)
	child := exec.Command(os.Args[0], "-test.run=^TestNativeProviderCrashOwner$", "-test.v")
	child.Env = append(os.Environ(), "TW_MA1_CRASH_CHILD=1")
	log, err := os.Create(filepath.Join(root, "crashed-owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	child.Stdout, child.Stderr = log, log
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "release"), []byte("settle\n"), 0600)
		_ = child.Process.Kill()
	})
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(root, "attempts")); return err == nil })
	_ = child.Process.Kill()
	_ = child.Wait()
	base := filepath.Join(root, "crashed-owner")
	if guard, err := hostmutation.Lock(base); err == nil {
		guard.Close()
		t.Fatal("native helper lost inherited installation exclusion after owner exit")
	}
	if err = os.WriteFile(filepath.Join(root, "release"), []byte("settle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var guard *os.File
	waitFor(t, func() bool { guard, err = hostmutation.Lock(base); return err == nil })
	defer guard.Close()
	j, err := maintenance.Load(base)
	if err != nil || j.Phase != "applying" || j.Start == nil {
		t.Fatal("one-use intent lost after owner exit", err)
	}
	p := &Provider{Base: base, Guard: guard, Action: j.Intent.Action}
	for range 2 {
		result, err := p.Recover(*j.PlanSHA, *j.Start)
		if err != nil || !result.LocalVerified {
			t.Fatal("fsynced native receipt did not survive owner exit", err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "attempts")); err != nil || string(raw) != "attempt\n" {
		t.Fatal("recovery repeated the native install", string(raw), err)
	}
}
