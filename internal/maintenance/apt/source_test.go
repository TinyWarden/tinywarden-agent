package apt

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestEmbeddedAdapterSyntaxAndClosedDispatch(t *testing.T) {
	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	// Compile without importing apt, taking native locks or executing host commands.
	check := exec.Command("/usr/bin/python3", "-I", "-c", "import sys; compile(sys.stdin.read(), '<embedded-maintenance>', 'exec')")
	check.Stdin = strings.NewReader(source)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("invalid embedded adapter: %v %s", err, out)
	}
}

func TestCanonicalPlanMatchesIndependentFixture(t *testing.T) {
	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../testdata/maintenance-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Plan   json.RawMessage `json:"plan"`
		Digest string          `json:"provider_sha256"`
	}
	if err := json.Unmarshal(fixture, &value); err != nil {
		t.Fatal(err)
	}
	// Execute only pure canonical/hash functions; no apt dependency or host access.
	code := "import ast,json,hashlib,sys; tree=ast.parse(sys.stdin.read()); funcs=[n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name in ('canonical','digest')]; exec(compile(ast.Module(body=funcs,type_ignores=[]),'<pure-contract>','exec')); print(digest(json.loads(sys.argv[1])))"
	check := exec.Command("/usr/bin/python3", "-I", "-c", code, string(value.Plan))
	check.Stdin = strings.NewReader(source)
	out, err := check.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != value.Digest {
		t.Fatalf("canonical contract mismatch: %v %s", err, out)
	}
}

func TestDebianBootAndArchivePolicy(t *testing.T) {
	script, err := os.ReadFile("testdata/policy_test.py")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.Command("/usr/bin/python3", "-I", "-c", string(script), "helper")
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("maintenance policy failed: %v %s", err, out)
	}
}

func TestDebianReaderDeadlinesAndMultiarch(t *testing.T) {
	script, err := os.ReadFile("testdata/deadline_test.py")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.Command("/usr/bin/python3", "-I", "-c", string(script), "helper")
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("maintenance reader policy failed: %v %s", err, out)
	}
}
