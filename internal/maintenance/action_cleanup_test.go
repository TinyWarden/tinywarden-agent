package maintenance

import (
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"os"
	"path/filepath"
	"testing"
)

func TestArchivedActionReleasesOnlySettledStagingAndCache(t *testing.T) {
	c, j, _ := acknowledgedFixture(t)
	i := actionIntent(t)
	i.ID = j.Intent.ID
	i.Scope = j.Intent.Scope
	j.Intent = i
	j.Version = 2
	if e := Save(c.Base, *j); e != nil {
		t.Fatal(e)
	}
	private := filepath.Join(c.Base, "updater", "private")
	state := filepath.Join(private, "action-state-"+j.Intent.ID)
	cache := filepath.Join(private, "action-packages", i.Binding.ContentSHA)
	for _, path := range []string{state, cache} {
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(path, "data"), []byte("retained"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	receipt, e := ArtifactPath(c.Base, j.Intent.ID, "action_apply")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(receipt, []byte("immutable receipt"), 0600); e != nil {
		t.Fatal(e)
	}
	if cleanupArchivedAction(c.Base, *j) == nil {
		t.Fatal("active fence allowed cleanup")
	}
	if _, e = os.Stat(state); e != nil {
		t.Fatal("pending state removed", e)
	}
	if e = hostmutation.ClearFence(c.Base, j.Intent.Fence()); e != nil {
		t.Fatal(e)
	}
	if e = archiveAcknowledged(c.Base, *j); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{state, cache} {
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("settled staging retained", path, e)
		}
	}
	if raw, e := os.ReadFile(receipt); e != nil || string(raw) != "immutable receipt" {
		t.Fatal("apply receipt lost", e)
	}
	if _, e = os.Stat(filepath.Join(private, "maintenance-history", j.Intent.ID+".json")); e != nil {
		t.Fatal("replay history lost", e)
	}
}
func TestActionCleanupPreservesUncertainAndRecoveryWork(t *testing.T) {
	for _, kind := range []string{"attention", "unacknowledged", "recovery"} {
		t.Run(kind, func(t *testing.T) {
			c, j, _ := acknowledgedFixture(t)
			if e := hostmutation.ClearFence(c.Base, j.Intent.Fence()); e != nil {
				t.Fatal(e)
			}
			state := filepath.Join(c.Base, "updater", "private", "action-state-"+j.Intent.ID)
			if e := os.Mkdir(state, 0700); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "attention":
				j.Attention = true
			case "unacknowledged":
				j.Acknowledged = false
			case "recovery":
				if e := hostmutation.PrivateWrite(recoveryPath(c.Base), []byte("{")); e != nil {
					t.Fatal(e)
				}
			}
			if cleanupArchivedAction(c.Base, *j) == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if _, e := os.Stat(state); e != nil {
				t.Fatal("uncertain staging removed", e)
			}
		})
	}
}
func TestActionCleanupDoesNotFollowWorkspaceLinks(t *testing.T) {
	c, j, _ := acknowledgedFixture(t)
	if e := hostmutation.ClearFence(c.Base, j.Intent.Fence()); e != nil {
		t.Fatal(e)
	}
	victim := t.TempDir()
	path := filepath.Join(victim, "retained")
	if e := os.WriteFile(path, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(c.Base, "updater", "private", "action-state-"+j.Intent.ID)
	if e := os.Symlink(victim, state); e != nil {
		t.Fatal(e)
	}
	if e := cleanupArchivedAction(c.Base, *j); e != nil {
		t.Fatal(e)
	}
	if raw, e := os.ReadFile(path); e != nil || string(raw) != "keep" {
		t.Fatal("cleanup followed a link", e)
	}
}
