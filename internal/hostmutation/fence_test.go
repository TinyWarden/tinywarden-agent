package hostmutation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFenceIdentityIsClosed(t *testing.T) {
	f := Fence{1, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", 1}
	if !f.Valid() {
		t.Fatal("valid scope refused")
	}
	f.Generation = 0
	if f.Valid() {
		t.Fatal("invalid generation admitted")
	}
}
func TestDurableFenceSurvivesLockReleaseAndRefusesReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on designated disposable host")
	}
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	f := Fence{1, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", 1}
	if err = WriteFence(base, f); err != nil {
		t.Fatal(err)
	}
	guard.Close()
	guard, err = Lock(base)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if AssertIdle(base) == nil {
		t.Fatal("released lock erased durable maintenance fence")
	}
	other := f
	other.Generation = 2
	if WriteFence(base, other) == nil || ClearFence(base, other) == nil {
		t.Fatal("replaced another generation's unresolved work")
	}
	if err = ClearFence(base, f); err != nil {
		t.Fatal(err)
	}
	if err = AssertIdle(base); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f)
	raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
	if err = os.WriteFile(fencePath(base), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if AssertIdle(base) == nil {
		t.Fatal("corrupt or duplicate journal became idle")
	}
}
