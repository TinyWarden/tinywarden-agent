package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/TinyWarden/tinywarden/agent/internal/agent"
	"github.com/TinyWarden/tinywarden/agent/internal/runner"
)

//go:embed en.json
var english []byte

type catalog struct {
	Name             string `json:"name"`
	Usage            string `json:"usage"`
	NotReady         string `json:"notReady"`
	InvalidArguments string `json:"invalidArguments"`
	Enrolled         string `json:"enrolled"`
	Replaced         string `json:"replaced"`
	Running          string `json:"running"`
	Degraded         string `json:"degraded"`
	Recovered        string `json:"recovered"`
	CommandFailed    string `json:"commandFailed"`
}

const Version = "0.0.1"

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == runner.SupervisorArgument {
		return runner.Supervise()
	}
	if len(args) == 1 && args[0] == "__collect-disk-v1" {
		if err := agent.CollectDisk(stdout); err != nil {
			return 1
		}
		return 0
	}
	var text catalog
	if err := json.Unmarshal(english, &text); err != nil {
		panic(err)
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
		config, err := agent.LoadConfig(args[2])
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
		config, err := agent.LoadConfig(args[2])
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
		config, err := agent.LoadConfig(args[2])
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
