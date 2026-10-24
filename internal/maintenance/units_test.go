package maintenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedWorkerHasIndependentLifetime(t *testing.T) {
	names := []string{"tinywarden-agent-maintenance.service", "tinywarden-agent-maintenance.timer", "tinywarden-agent-maintenance-worker@.service"}
	dir := t.TempDir()
	args := []string{"verify"}
	for _, name := range names {
		raw, e := os.ReadFile(filepath.Join("../../deploy/systemd/managed", name))
		if e != nil {
			t.Fatal(e)
		}
		value := strings.ReplaceAll(string(raw), "@BASE@", "/usr")
		value = strings.ReplaceAll(value, "/usr/current/tinywarden-agent", "/bin/true")
		if strings.Contains(name, "worker@") {
			for _, property := range []string{"Type=exec", "ExitType=cgroup", "TimeoutStopSec=infinity", "TimeoutStartSec=infinity", "RuntimeMaxSec=infinity", "SendSIGKILL=no", "Restart=no", "KillMode=mixed"} {
				if !strings.Contains(value, property+"\n") {
					t.Fatal("unsafe worker lifetime", property)
				}
			}
			if strings.Contains(value, "PartOf=") || strings.Contains(value, "BindsTo=") {
				t.Fatal("monitoring lifetime controls root worker")
			}
		}
		path := filepath.Join(dir, name)
		if e = os.WriteFile(path, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		args = append(args, path)
	}
	if out, e := exec.Command("/usr/bin/systemd-analyze", args...).CombinedOutput(); e != nil {
		t.Fatalf("invalid maintenance templates: %v %s", e, out)
	}
}
