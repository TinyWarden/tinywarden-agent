package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

func TestMaintenanceOptionalTemplatesAreComplete(t *testing.T) {
	release := t.TempDir()
	if e := os.Mkdir(filepath.Join(release, "systemd"), 0755); e != nil {
		t.Fatal(e)
	}
	names, supported, e := templateSet(release)
	if e != nil || supported || len(names) != 3 {
		t.Fatal("old rollback rejected", e)
	}
	for i, name := range maintenanceUnits {
		if e = os.WriteFile(filepath.Join(release, "systemd", name), []byte("fixture"), 0644); e != nil {
			t.Fatal(e)
		}
		names, supported, e = templateSet(release)
		if i < 2 && e == nil {
			t.Fatal("partial template set admitted")
		}
		if i == 2 && (e != nil || !supported || len(names) != 6) {
			t.Fatal("complete template set rejected", e)
		}
	}
}

func TestMaintenanceRunningDigestBindsExecutingInode(t *testing.T) {
	path, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	expected := sha256.Sum256(raw)
	actual, e := runningDigest()
	if e != nil || actual != hex.EncodeToString(expected[:]) {
		t.Fatal("executing inode did not match trusted bytes", e)
	}
}
func TestRecoveryAuthorizationInputStaysOutOfArguments(t *testing.T) {
	token := "tw_recovery_v1.55555555-5555-4555-8555-555555555555." + strings.Repeat("c", 43)
	messages := MaintenanceMessages{RecoveryInput: "invalid authorization"}
	got, e := readRecoveryAuthorization(strings.NewReader(token+"\n"), messages)
	if e != nil || got != token {
		t.Fatal("valid stdin refused", e)
	}
	for _, input := range []string{"", token + "\n" + token, strings.Repeat("x", 300), token + " ", "https://example.invalid/" + token} {
		if _, e = readRecoveryAuthorization(strings.NewReader(input), messages); e == nil {
			t.Fatal("unbounded or invalid input admitted")
		}
	}
}

// An external driver temporarily reserves these exact names and restores the
// existing test agent registrations. Never run by ordinary local/root checks.
func TestNativeMaintenanceUnitTransitions(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("TW_MA1_NATIVE_UNITS") != "1" {
		t.Skip("requires explicit expendable-host registration rehearsal")
	}
	ctx := context.Background()
	base := t.TempDir()
	if e := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); e != nil {
		t.Fatal(e)
	}
	old := filepath.Join(base, "old")
	next := filepath.Join(base, "new")
	stage := func(release string, extra bool) {
		t.Helper()
		if e := os.MkdirAll(filepath.Join(release, "systemd"), 0755); e != nil {
			t.Fatal(e)
		}
		names := []string{Service, UpdateService, Timer}
		if extra {
			names = append(names, maintenanceUnits...)
		}
		for _, name := range names {
			raw, e := os.ReadFile(filepath.Join("../../deploy/systemd/managed", name))
			if e != nil {
				t.Fatal(e)
			}
			lines := strings.Split(string(raw), "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "ExecStart=") {
					lines[i] = "ExecStart=/usr/bin/true"
				}
			}
			if e = os.WriteFile(filepath.Join(release, "systemd", name), []byte(strings.Join(lines, "\n")), 0644); e != nil {
				t.Fatal(e)
			}
		}
	}
	stage(old, false)
	stage(next, true)
	workerScript := filepath.Join(base, "worker.py")
	workerCode := `import fcntl,os,sys,time
from pathlib import Path
base=Path(sys.argv[1])
fd=os.open(base/'updater/private/lock',os.O_RDWR|os.O_CREAT,0o600)
fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
if os.fork():
 os._exit(0)
(base/'locked').write_text('ready')
time.sleep(1)
os.close(fd)
(base/'unlocked').write_text('ready')
time.sleep(2)
`
	if e := os.WriteFile(workerScript, []byte(workerCode), 0600); e != nil {
		t.Fatal(e)
	}
	workerTemplate := filepath.Join(next, "systemd", MaintenanceWorker)
	raw, e := os.ReadFile(workerTemplate)
	if e != nil {
		t.Fatal(e)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "ExecStart=/usr/bin/true", "ExecStart=/usr/bin/python3 -I -B \""+unitQuote(workerScript)+"\" \""+unitQuote(base)+"\""))
	if e = os.WriteFile(workerTemplate, raw, 0644); e != nil {
		t.Fatal(e)
	}
	all := append([]string{Service, UpdateService, Timer}, maintenanceUnits...)
	for _, name := range all {
		if _, e := os.Lstat(filepath.Join("/etc/systemd/system", name)); !os.IsNotExist(e) {
			t.Fatal("native driver did not reserve unit names", name)
		}
	}
	t.Cleanup(func() {
		_ = system(ctx, "disable", "--now", MaintenanceTimer)
		_ = system(ctx, "stop", MaintenanceService)
		for _, name := range all {
			if ownedRegistration(base, name) == nil {
				_ = os.Remove(filepath.Join("/etc/systemd/system", name))
			}
		}
		_ = system(ctx, "daemon-reload")
	})
	if e := units(ctx, base, old); e != nil {
		t.Fatal("old registration", e)
	}
	if e := bootstrapVerifiedMaintenance(ctx, base, next); e != nil {
		t.Fatal("three-to-six bootstrap", e)
	}
	if e := system(ctx, "is-active", "--quiet", MaintenanceTimer); e != nil {
		t.Fatal("maintenance polling did not activate", e)
	}
	for _, name := range all {
		if e := ownedRegistration(base, name); e != nil {
			t.Fatal(e)
		}
	}
	worker := "tinywarden-agent-maintenance-worker@11111111-1111-4111-8111-111111111111.service"
	if e := system(ctx, "start", "--no-block", worker); e != nil {
		t.Fatal(e)
	}
	waitFor := func(predicate func() bool) {
		t.Helper()
		until := time.Now().Add(8 * time.Second)
		for !predicate() {
			if time.Now().After(until) {
				t.Fatal("native worker state did not settle")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	waitFor(func() bool { _, e := os.Stat(filepath.Join(base, "locked")); return e == nil })
	if guard, e := hostmutation.Lock(base); !errors.Is(e, hostmutation.ErrBusy) {
		if guard != nil {
			guard.Close()
		}
		t.Fatal("descendant did not inherit root mutation guard", e)
	}
	waitFor(func() bool { _, e := os.Stat(filepath.Join(base, "unlocked")); return e == nil })
	guard, e := hostmutation.Lock(base)
	if e != nil {
		t.Fatal("native descendant retained a closed guard", e)
	}
	guard.Close()
	if guard, e = lock(base); !errors.Is(e, hostmutation.ErrBusy) {
		if guard != nil {
			guard.Close()
		}
		t.Fatal("lingering cgroup did not fence installer after lock loss", e)
	}
	waitFor(func() bool { active, e := activeMaintenanceWorkers(""); return e == nil && !active })
	if guard, e = lock(base); e != nil {
		t.Fatal("completed descendant did not release installer", e)
	}
	guard.Close()
	if e := system(ctx, "disable", "--now", MaintenanceTimer); e != nil {
		t.Fatal(e)
	}
	if e := bootstrapVerifiedMaintenance(ctx, base, next); e != nil {
		t.Fatal(e)
	}
	if system(ctx, "is-enabled", "--quiet", MaintenanceTimer) == nil {
		t.Fatal("bootstrap reset operator disabled timer")
	}
	if e := units(ctx, base, old); e != nil {
		t.Fatal("six-to-three rollback", e)
	}
	for _, name := range maintenanceUnits {
		if _, e := os.Lstat(filepath.Join("/etc/systemd/system", name)); !os.IsNotExist(e) {
			t.Fatal("rollback retained new registration", name)
		}
	}
	conflict := filepath.Join("/etc/systemd/system", MaintenanceWorker)
	if e := os.WriteFile(conflict, []byte("fixture conflicting registry\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := units(ctx, base, next); e == nil {
		t.Fatal("foreign registration overwritten")
	}
	for _, name := range maintenanceUnits[:2] {
		if _, e := os.Lstat(filepath.Join("/etc/systemd/system", name)); !os.IsNotExist(e) {
			t.Fatal("conflict created partial registrations")
		}
	}
	if e := os.Remove(conflict); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(old, "systemd", MaintenanceService), []byte("partial"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := units(ctx, base, old); e == nil {
		t.Fatal("partial signed bundle activated")
	}
	if e := os.Remove(filepath.Join(old, "systemd", MaintenanceService)); e != nil {
		t.Fatal(e)
	}
	if e := bootstrapVerifiedMaintenance(ctx, base, next); e != nil {
		t.Fatal(e)
	}
	cfg := agentconfig.Config{ControlPlaneOrigin: "https://control.example", StateDir: filepath.Join(base, "data"), SkillOperations: []string{}, Managed: true,
		Updates: &agentrelease.UpdateSettings{HostID: "22222222-2222-4222-8222-222222222222", AgentID: "33333333-3333-4333-8333-333333333333"}}
	if e := atomicFile(filepath.Join(base, "config.json"), cfg, 0600); e != nil {
		t.Fatal(e)
	}
	outbox := filepath.Join(base, "updater", "private", "maintenance-recovery.json")
	if e := os.WriteFile(outbox, []byte(`{"broken":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := Uninstall(ctx, base, "purge"); e == nil {
		t.Fatal("uninstall discarded unresolved recovery")
	}
	if e := system(ctx, "is-active", "--quiet", MaintenanceTimer); e != nil {
		t.Fatal("blocked uninstall changed units", e)
	}
	if e := os.Remove(outbox); e != nil {
		t.Fatal(e)
	}
	if e := Uninstall(ctx, base, "keep"); e != nil {
		t.Fatal("keep-data uninstall", e)
	}
	if _, e := os.Stat(filepath.Join(base, "config.json")); e != nil {
		t.Fatal("keep-data uninstall lost configuration", e)
	}
	if e := units(ctx, base, next); e != nil {
		t.Fatal("restore kept registration", e)
	}
	if e := Uninstall(ctx, base, "purge"); e != nil {
		t.Fatal("purge-data uninstall", e)
	}
	if _, e := os.Stat(base); !os.IsNotExist(e) {
		t.Fatal("purge-data uninstall retained local fixture")
	}
}
