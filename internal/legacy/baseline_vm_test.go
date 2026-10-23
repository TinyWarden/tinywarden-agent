package legacy_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	agent "github.com/TinyWarden/tinywarden-agent/internal/agent"
	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	host "github.com/TinyWarden/tinywarden-agent/internal/host"
	"github.com/TinyWarden/tinywarden-agent/internal/host/filesystems"
	"github.com/TinyWarden/tinywarden-agent/internal/legacy/runner"
)

// Test binaries need the same fixed supervisor entry as the real CLI. This is
// test-only dispatch, without extending product recipe or transport authority.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == runner.SupervisorArgument {
		os.Exit(runner.Supervise())
	}
	if len(os.Args) == 2 && os.Args[1] == "__collect-disk-v1" {
		if filesystems.CollectDisk(os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Explicit disposable acceptance harness. It runs the ordinary production Run
// and native recipes under a dedicated temporary state directory; the installed
// VM service, credentials and system trust store are untouched.
func TestBaselineDisposableVM(t *testing.T) {
	file := os.Getenv("TW_P3C_ACCEPTANCE_CONFIG")
	if file == "" {
		t.Skip("explicit disposable VM acceptance only")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("acceptance config unavailable")
	}
	var config struct {
		Origin   string `json:"origin"`
		CAFile   string `json:"ca_file"`
		StateDir string `json:"state_dir"`
	}
	if json.Unmarshal(data, &config) != nil || !host.CanObserve() {
		t.Fatal("unsupported acceptance context")
	}
	ca, err := os.ReadFile(config.CAFile)
	if err != nil {
		t.Fatal("acceptance CA unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("invalid acceptance CA")
	}
	client := agent.NewClient(config.Origin)
	client.HTTP.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	err = agent.Run(ctx, agentconfig.Config{ControlPlaneOrigin: config.Origin, StateDir: config.StateDir}, client, func(event string) {
		if event == "baseline_unavailable" || event == "baseline_state_unavailable" || event == "disk_unavailable" {
			t.Error("connected acceptance lane unavailable", event)
		}
	})
	if err != context.DeadlineExceeded {
		t.Fatal("acceptance run stopped early", controlplane.ErrorCategory(err))
	}
}
