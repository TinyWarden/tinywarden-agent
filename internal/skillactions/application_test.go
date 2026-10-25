package skillactions

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	"github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
)

// Opt-in real application/HTTPS/package/native proof on the designated expendable
// host. The private app fixture supplies recognized platform evidence separately;
// this does not claim signed managed-installation or running-inode admission.
func TestDeliveredActionApplication(t *testing.T) {
	config := os.Getenv("TW_ACTION_APP")
	if config == "" {
		t.Skip("explicit disposable-host application scope required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required")
	}
	info, err := os.Lstat(config)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private fixture config required")
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Origin     string                 `json:"origin"`
		Credential string                 `json:"credential"`
		CA         string                 `json:"ca"`
		Base       string                 `json:"base"`
		Runtime    string                 `json:"runtime"`
		Policy     string                 `json:"policy"`
		Scope      maintenance.Scope      `json:"scope"`
		Probes     []actioncontract.Probe `json:"probes"`
	}
	if controlplane.Decode(raw, &fixture) != nil || !strings.HasPrefix(fixture.Base, "/var/lib/tw-action-sdk-test/") {
		t.Fatal("invalid disposable fixture")
	}
	if err = os.MkdirAll(filepath.Join(fixture.Base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := hostmutation.Lock(fixture.Base)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	client := controlplane.NewClient(fixture.Origin)
	ca, err := os.ReadFile(fixture.CA)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA")
	}
	client.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	loader := Loader{Base: fixture.Base, Policy: fixture.Policy, Guard: guard, Runtime: skillruntime.Client{Assets: fixture.Runtime}, Verify: func() error { return nil },
		Download: func(ctx context.Context, intent maintenance.Intent, store string) (string, error) {
			return client.FetchArtifact(ctx, fixture.Credential, "/api/v1/agent/maintenance-jobs/"+intent.ID+"/package", intent.Binding.ContentSHA, store, *intent.Archive)
		}}
	registry := loader.Registry()
	if os.Getenv("TW_ACTION_APP_STAGE") == "reconcile" {
		// Exercise the production recovery transport and pinned inspector. The
		// fixture identity has the same admission limitation documented above.
		token, e := os.ReadFile(os.Getenv("TW_ACTION_RECOVERY_TOKEN_PATH"))
		if e != nil {
			t.Fatal(e)
		}
		recovery := maintenance.RecoveryCoordinator{Base: fixture.Base, Registry: registry,
			Call: maintenance.RecoveryPost(client), Verify: func(scope maintenance.RecoveryScope) error {
				if scope.ScopeValue() != fixture.Scope {
					t.Fatal("recovery fixture scope mismatch")
				}
				return nil
			}}
		if e = recovery.Reconcile(context.Background(), strings.TrimSpace(string(token))); e != nil {
			t.Fatal(e)
		}
		t.Log("Production read-only recovery completed; retained failure was not executed again")
		return
	}
	if os.Getenv("TW_ACTION_APP_STAGE") == "replay" {
		journal, e := maintenance.Load(fixture.Base)
		if e != nil || journal.PlanSHA == nil || journal.Start == nil || journal.Result == nil {
			t.Fatal("saved application result required", e)
		}
		definition, e := registry.ResolveIntent(journal.Intent)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(fixture.Base, "updater", "private", "maintenance-"+journal.Intent.ID+"-action_apply.json")
		before, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		result, e := definition.New().Apply(*journal.PlanSHA, *journal.Start)
		if journal.Attention {
			if e == nil {
				t.Fatal("uncertain application replay accepted")
			}
		} else if e != nil || !result.LocalVerified || !sameApplicationFacts(result.Facts, journal.Result.Facts) {
			t.Fatal("successful application replay did not recover original receipt", e)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(before, after) {
			t.Fatal("application replay changed immutable receipt", readErr)
		}
		t.Log("Application replay retained its original immutable receipt without repeating execution")
		return
	}
	if os.Getenv("TW_ACTION_APP_STAGE") == "receipt" {
		journal, e := maintenance.Load(fixture.Base)
		if e != nil || journal.PlanSHA == nil || journal.Start == nil {
			t.Fatal("saved apply receipt required", e)
		}
		definition, e := registry.ResolveIntent(journal.Intent)
		if e != nil {
			t.Fatal(e)
		}
		result, e := definition.New().Recover(*journal.PlanSHA, *journal.Start)
		if e != nil || !result.LocalVerified || result.ProcessRunning || !sameApplicationFacts(result.Facts, journal.Result.Facts) {
			t.Fatal("receipt verification without execution", e)
		}
		t.Logf("verified original receipt facts=%s", result.Facts)
		return
	}
	coordinator := maintenance.Coordinator{Base: fixture.Base, Scope: fixture.Scope, Schema: 2, Registry: registry,
		Call: func(ctx context.Context, request maintenance.Request) (maintenance.Reply, error) {
			if request.JobID == nil {
				input, _ := json.Marshal(map[string]any{"probes": fixture.Probes})
				cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", filepath.Join(fixture.Runtime, "actions", "readiness.py"), fixture.Policy)
				cmd.Stdin = bytes.NewReader(input)
				result, e := cmd.Output()
				if e != nil {
					return maintenance.Reply{}, e
				}
				var readiness struct {
					Origins []actioncontract.OriginPermission `json:"origins"`
					Actions []actioncontract.Readiness        `json:"actions"`
				}
				if e = controlplane.Decode(result, &readiness); e != nil {
					return maintenance.Reply{}, e
				}
				request.Data, _ = json.Marshal(map[string]any{"probe_cursor": nil, "worker_ready": true, "origins": readiness.Origins, "readiness": readiness.Actions})
			}
			return maintenance.Post(ctx, client, fixture.Credential, request, registry)
		}}
	ctx := context.Background()
	if os.Getenv("TW_ACTION_APP_STAGE") == "inspect" || os.Getenv("TW_ACTION_APP_STAGE") == "inspect_refused" {
		journal, e := maintenance.Load(fixture.Base)
		if e != nil || journal.Result == nil {
			t.Fatal("saved result required", e)
		}
		definition, e := registry.ResolveIntent(journal.Intent)
		if e != nil {
			t.Fatal(e)
		}
		inspector, ok := definition.New().(maintenance.ActionInspector)
		if !ok {
			t.Fatal("package inspector required")
		}
		called := false
		if e = inspector.InspectAction(ctx, func(facts json.RawMessage) error {
			if called || !sameApplicationFacts(facts, journal.Result.Facts) {
				t.Fatal("inspection did not preserve saved facts")
			}
			called = true
			t.Logf("package inspection facts=%s", facts)
			return nil
		}); os.Getenv("TW_ACTION_APP_STAGE") == "inspect_refused" {
			if e == nil || called {
				t.Fatal("unsafe package state accepted for recovery", e)
			}
			t.Log("Read-only inspection refused unsafe package state; no recovery committed")
		} else if e != nil || !called {
			t.Fatal("read-only package inspection", e)
		}
		return
	}
	if os.Getenv("TW_ACTION_APP_STAGE") == "ready" {
		_, err = coordinator.Call(ctx, maintenance.Request{Schema: 2, HostID: fixture.Scope.HostID, AgentID: fixture.Scope.AgentID, Generation: fixture.Scope.Generation, Kind: "poll"})
		if err != nil {
			t.Fatal("real readiness poll", err)
		}
		return
	}
	if prior, e := maintenance.Load(fixture.Base); e == nil && prior.Acknowledged {
		if prior.Result == nil || prior.Attention || !prior.Result.LocalVerified {
			t.Fatal("invalid saved terminal result")
		}
		t.Log("Existing terminal receipt retained without execution")
		return
	}
	journal, err := coordinator.Discover(ctx)
	if err != nil || journal == nil {
		t.Fatal("discover real app intent", err)
	}
	if err = coordinator.Work(ctx, journal); err != nil {
		t.Fatal("real application worker", err)
	}
	current, err := maintenance.Load(fixture.Base)
	if err != nil {
		t.Fatal(err)
	}
	if current.Result != nil {
		t.Logf("phase=%s acknowledged=%t attention=%t result=%s", current.Phase, current.Acknowledged, current.Attention, current.Result.Facts)
	}
}

// JSON object member order is not part of the package's typed facts contract.
func sameApplicationFacts(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) any {
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&value) != nil {
			return nil
		}
		return value
	}
	left, right := decode(a), decode(b)
	return left != nil && right != nil && reflect.DeepEqual(left, right)
}
