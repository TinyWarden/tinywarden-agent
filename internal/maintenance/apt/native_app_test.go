package apt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

// Opt-in real application transport proof. A designated disposable fixture owns
// the signed HTTP package repository and a private test-app credential/CA file.
// This is not signed managed installation or running-inode admission evidence.
func TestNativeApplicationCoordinator(t *testing.T) {
	root := nativeFixture(t)
	configPath := os.Getenv("TW_MA1_NATIVE_APP")
	if configPath == "" {
		t.Skip("requires isolated application bridge")
	}
	info, err := os.Lstat(configPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private application fixture required")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Origin     string            `json:"origin"`
		Credential string            `json:"credential"`
		CA         string            `json:"ca"`
		Base       string            `json:"base"`
		Scope      maintenance.Scope `json:"scope"`
	}
	if controlplane.Decode(raw, &fixture) != nil || !strings.HasPrefix(fixture.Base, root+string(os.PathSeparator)) {
		t.Fatal("invalid application fixture")
	}
	if err = os.MkdirAll(filepath.Join(fixture.Base, "updater", "private"), 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := hostmutation.Lock(fixture.Base)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	expected := os.Getenv("TW_MA1_EXPECT_PHASE")
	settled := func(current *maintenance.Journal, err error) {
		t.Helper()
		if err != nil || current.Intent.Scope != fixture.Scope || !current.Acknowledged || current.Phase != "terminal" || current.Attention || current.Result == nil || !current.Result.LocalVerified || current.Result.ProcessRunning {
			t.Fatal("terminal receipt not preserved", err)
		}
		if fence, err := hostmutation.ReadFence(fixture.Base); err != nil || fence != nil {
			t.Fatal("settled local fence retained", err)
		}
	}
	// A terminal receipt is intentionally retained. Rechecking it must not poll
	// a new intent or repeat native installation.
	if current, err := maintenance.Load(fixture.Base); err == nil && current.Acknowledged && expected == "settled" {
		settled(current, err)
		return
	}
	registry := Registry(fixture.Base, guard, nil)
	client := controlplane.NewClient(fixture.Origin)
	ca, err := os.ReadFile(fixture.CA)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid private fixture CA")
	}
	// Keep normal HTTPS verification; add only this private app fixture trust root.
	client.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	coordinator := maintenance.Coordinator{Base: fixture.Base, Scope: fixture.Scope, Registry: registry, Call: func(ctx context.Context, r maintenance.Request) (maintenance.Reply, error) {
		return maintenance.Post(ctx, client, fixture.Credential, r, registry)
	}}
	journal, err := coordinator.Discover(context.Background())
	if err != nil || journal == nil {
		t.Fatal("discover real app intent", err)
	}
	if err = coordinator.Work(context.Background(), journal); err != nil {
		t.Fatal("real app/native worker", err)
	}
	current, err := maintenance.Load(fixture.Base)
	if expected == "settled" {
		settled(current, err)
		return
	}
	if err != nil || current.Phase != expected {
		t.Fatal("unexpected saved phase", expected, current, err)
	}
}
