package installation

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

type MaintenanceMessages struct {
	Root                    string `json:"root"`
	Arguments               string `json:"arguments"`
	ConfigHelp              string `json:"configHelp"`
	PollHelp                string `json:"pollHelp"`
	JobHelp                 string `json:"jobHelp"`
	StatusHelp              string `json:"statusHelp"`
	ReconcileHelp           string `json:"reconcileHelp"`
	AuthorizationStdinHelp  string `json:"authorizationStdinHelp"`
	AuthorizationPromptHelp string `json:"authorizationPromptHelp"`
	RecoveryPrompt          string `json:"recoveryPrompt"`
	RecoveryInput           string `json:"recoveryInput"`
}

func DispatchMaintenance(ctx context.Context, args []string, out, diagnostics io.Writer, text MaintenanceMessages) (bool, error) {
	if len(args) == 0 || args[0] != "maintenance" {
		return false, nil
	}
	if os.Geteuid() != 0 {
		return true, errors.New(text.Root)
	}
	flags := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	configPath := flags.String("config", "", text.ConfigHelp)
	poll := flags.Bool("poll", false, text.PollHelp)
	job := flags.String("job", "", text.JobHelp)
	status := flags.Bool("status", false, text.StatusHelp)
	reconcile := flags.Bool("reconcile", false, text.ReconcileHelp)
	authorizationStdin := flags.Bool("authorization-stdin", false, text.AuthorizationStdinHelp)
	authorizationPrompt := flags.Bool("authorization-prompt", false, text.AuthorizationPromptHelp)
	if e := flags.Parse(args[1:]); e != nil {
		return true, e
	}
	modes := 0
	if *poll {
		modes++
	}
	if *job != "" {
		modes++
	}
	if *status {
		modes++
	}
	if *reconcile {
		modes++
	}
	if flags.NArg() != 0 || modes != 1 || (*authorizationStdin || *authorizationPrompt) && !*reconcile || *authorizationStdin && *authorizationPrompt || filepath.Base(*configPath) != "config.json" {
		return true, errors.New(text.Arguments)
	}
	base := filepath.Dir(*configPath)
	if e := basePath(base); e != nil {
		return true, e
	}
	if *reconcile {
		var input io.Reader
		if *authorizationStdin {
			input = os.Stdin
		}
		return true, ReconcileMaintenance(ctx, base, input, *authorizationPrompt, text)
	}
	if *status {
		j, e := maintenance.Load(base)
		if e != nil {
			return true, e
		}
		return true, json.NewEncoder(out).Encode(j)
	}
	if *poll {
		return true, PollMaintenance(ctx, base)
	}
	return true, WorkMaintenance(ctx, base, *job)
}
