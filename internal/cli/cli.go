package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/agent"
	loop "github.com/TinyWarden/tinywarden-agent/internal/agent/loop"
	agentconfig "github.com/TinyWarden/tinywarden-agent/internal/config"
	"github.com/TinyWarden/tinywarden-agent/internal/host/filesystems"
	"github.com/TinyWarden/tinywarden-agent/internal/installation"
)

//go:embed en.json
var english []byte

type catalog struct {
	Setup               installation.SetupMessages       `json:"setup"`
	Maintenance         installation.MaintenanceMessages `json:"maintenance"`
	Reconnect           installation.ReconnectMessages   `json:"reconnect"`
	Name                string                           `json:"name"`
	Usage               string                           `json:"usage"`
	NotReady            string                           `json:"notReady"`
	InvalidArguments    string                           `json:"invalidArguments"`
	Enrolled            string                           `json:"enrolled"`
	Replaced            string                           `json:"replaced"`
	Running             string                           `json:"running"`
	Degraded            string                           `json:"degraded"`
	Recovered           string                           `json:"recovered"`
	CommandFailed       string                           `json:"commandFailed"`
	HeartbeatDiagnostic string                           `json:"heartbeatDiagnostic"`
}

const Version = agent.Version

func Run(args []string, stdout, stderr io.Writer) int {
	var text catalog
	if err := json.Unmarshal(english, &text); err != nil {
		panic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if handled, err := installation.DispatchMaintenance(ctx, args, stdout, stderr, text.Maintenance); handled {
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "__remove-legacy" {
		if installation.RemoveLegacyData() != nil {
			return 1
		}
		return 0
	}
	if len(args) == 3 && args[0] == "__copy-legacy" && args[1] == "--destination" {
		if installation.CopyLegacy(args[2]) != nil {
			return 1
		}
		return 0
	}
	if handled, err := installation.Dispatch(ctx, args, stdout, stderr, text.Reconnect, text.Setup); handled {
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if os.Geteuid() == 0 && len(args) > 0 && (args[0] == "run" || args[0] == "enroll" || args[0] == "resume-enrollment" || args[0] == "replace" || args[0] == "__collect-disk-v1") {
		fmt.Fprintln(stderr, "Monitoring and skills must run as the dedicated non-root service account.")
		return 1
	}
	if len(args) == 1 && args[0] == "__collect-disk-v1" {
		if err := filesystems.CollectDisk(stdout); err != nil {
			return 1
		}
		return 0
	}
	if len(args) == 3 && args[0] == "resume-enrollment" && args[1] == "--config" {
		config, err := agentconfig.LoadConfig(args[2])
		if err == nil {
			err = agent.ResumeEnrollment(ctx, config, agent.NewClient(config.ControlPlaneOrigin))
		}
		if err != nil {
			fmt.Fprintln(stderr, text.CommandFailed)
			return 1
		}
		fmt.Fprintln(stdout, text.Enrolled)
		return 0
	}
	if len(args) == 1 {
		switch args[0] {
		case "-version", "--version":
			fmt.Fprintf(stdout, "%s %s\n", text.Name, Version)
			return 0
		case "-h", "--help":
			fmt.Fprintln(stdout, text.Usage)
			return 0
		}
	}
	if len(args) == 5 && args[0] == "enroll" && args[1] == "--config" &&
		args[3] == "--token-file" {
		config, err := agentconfig.LoadConfig(args[2])
		if err != nil {
			fmt.Fprintln(stderr, text.CommandFailed)
			return 1
		}
		client := agent.NewClient(config.ControlPlaneOrigin)
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := agent.StartEnrollment(ctx, config, args[4], client); err != nil {
			fmt.Fprintln(stderr, text.CommandFailed)
			return 1
		}
		fmt.Fprintln(stdout, text.Enrolled)
		return 0
	}
	if len(args) == 5 && args[0] == "replace" && args[1] == "--config" &&
		args[3] == "--token-file" {
		config, err := agentconfig.LoadConfig(args[2])
		if err != nil {
			fmt.Fprintln(stderr, text.CommandFailed)
			return 1
		}
		client := agent.NewClient(config.ControlPlaneOrigin)
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := agent.StartReplacement(ctx, config, args[4], client); err != nil {
			fmt.Fprintln(stderr, text.CommandFailed)
			return 1
		}
		fmt.Fprintln(stdout, text.Replaced)
		return 0
	}
	if len(args) == 3 && args[0] == "run" && args[1] == "--config" {
		config, err := agentconfig.LoadConfig(args[2])
		if err != nil {
			fmt.Fprintln(stderr, text.CommandFailed)
			return 1
		}
		client := agent.NewClient(config.ControlPlaneOrigin)
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		fmt.Fprintln(stdout, text.Running)
		err = agent.Run(ctx, config, client, func(event string) {
			if event == "degraded" {
				fmt.Fprintln(stderr, text.Degraded)
			}
			if event == "recovered" {
				fmt.Fprintln(stdout, text.Recovered)
			}
		}, func(diagnostic loop.HeartbeatDiagnostic) {
			fmt.Fprintf(stderr, "%s %s\n", text.HeartbeatDiagnostic, diagnostic.JSON())
		})
		if errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintln(stderr, text.CommandFailed)
		return 1
	}
	if len(args) > 0 {
		fmt.Fprintln(stderr, text.InvalidArguments)
	} else {
		fmt.Fprintln(stderr, text.NotReady)
	}
	return 2
}
