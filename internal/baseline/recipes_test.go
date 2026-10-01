package baseline

import (
	"encoding/json"
	"testing"

	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

func TestRecipesAreIndependentAndStrictlyAllowlisted(t *testing.T) {
	for _, key := range []Key{Packages, Reboot, Fstrim} {
		r, err := DefaultRecipe(key)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(r)
		if _, err = runner.Parse(encoded); err != nil {
			t.Fatal(key, err)
		}
		want := 10
		if key == Packages {
			want = 30
		}
		if r.TimeoutSeconds != want {
			t.Fatal("incorrect default budget", key)
		}
		r.Steps[0].Argv[0] = "--mutate"
		if _, err = recipeMode(key, r); err == nil {
			t.Fatal("mutation accepted", key)
		}
		fresh, _ := DefaultRecipe(key)
		if fresh.Steps[0].Argv[0] == "--mutate" {
			t.Fatal("shared recipe slice", key)
		}
	}
	for _, tc := range []struct {
		key    Key
		budget int
		mode   string
	}{
		{Packages, 0, "upgrade"}, {Packages, 31, "upgrade"}, {Packages, 30, "update"},
		{Reboot, 10, "with-new-pkgs"}, {Key("other"), 10, "upgrade"},
	} {
		if _, err := Recipe(tc.key, tc.budget, tc.mode); err == nil {
			t.Fatal("unsupported recipe accepted", tc)
		}
	}
	r, _ := Recipe(Packages, 30, "with-new-pkgs")
	if mode, err := recipeMode(Packages, r); err != nil || mode != "with-new-pkgs" {
		t.Fatal(mode, err)
	}
	r.SchemaVersion = 2
	if _, err := recipeMode(Packages, r); err == nil {
		t.Fatal("new schema accepted")
	}
	r, _ = DefaultRecipe(Reboot)
	r.Steps = append(r.Steps, r.Steps[0])
	if _, err := recipeMode(Reboot, r); err == nil {
		t.Fatal("extra step accepted")
	}
	r, _ = DefaultRecipe(Fstrim)
	r.Steps[0], r.Steps[1] = r.Steps[1], r.Steps[0]
	if _, err := recipeMode(Fstrim, r); err == nil {
		t.Fatal("wrong read order accepted")
	}
}
