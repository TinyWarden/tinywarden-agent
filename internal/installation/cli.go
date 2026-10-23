package installation

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

func Dispatch(ctx context.Context, args []string, out, diagnostics io.Writer, reconnectCopy ReconnectMessages) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	mode := args[0]
	if mode == "--uninstall" {
		mode = "uninstall"
	}
	switch mode {
	case "install", "update", "check", "repair", "reconnect", "auto-updates", "uninstall":
	default:
		return false, nil
	}
	if os.Geteuid() != 0 {
		return true, errors.New("this administrative command requires root; monitoring runs without root")
	}
	f := flag.NewFlagSet(mode, flag.ContinueOnError)
	f.SetOutput(diagnostics)
	base := f.String("install-dir", DefaultBase, "dedicated installation directory")
	configPath := f.String("config", "", "existing managed config.json")
	archive := f.String("archive", "", "verified release archive")
	manifest := f.String("manifest", "", "signed release manifest")
	signature := f.String("signature", "", "detached release signature")
	codeFile := f.String("setup-code-file", "", "root-owned mode-0600 setup code file")
	noAuto := f.Bool("no-auto-updates", false, "prohibit automatic updates locally")
	adopt := f.Bool("adopt", false, "migrate the known manual 0.0.7 installation")
	automatic := f.Bool("automatic", false, "scheduled policy-controlled update")
	keep := f.Bool("keep-data", false, "stop and disable services; keep files and data")
	purge := f.Bool("purge", false, "remove this local installation and its enrolled state")
	if e := f.Parse(args[1:]); e != nil {
		return true, e
	}
	if *configPath != "" {
		if filepath.Base(*configPath) != "config.json" {
			return true, errors.New("use the managed config.json")
		}
		*base = filepath.Dir(*configPath)
	}
	if e := basePath(*base); e != nil {
		return true, e
	}
	switch mode {
	case "install":
		if f.NArg() != 0 || *archive == "" || *manifest == "" || *signature == "" || *adopt && *codeFile != "" {
			return true, errors.New("install requires --archive, --manifest and --signature; adoption does not accept a setup code")
		}
		return true, Install(ctx, InstallOptions{Base: *base, Archive: *archive, Manifest: *manifest, Signature: *signature, CodeFile: *codeFile, Automatic: !*noAuto, Adopt: *adopt})
	case "reconnect":
		if f.NArg() != 0 || *archive != "" || *manifest != "" || *signature != "" || *adopt || *purge || *keep || *automatic || *noAuto {
			return true, errors.New(reconnectCopy.Arguments)
		}
		e := Reconnect(ctx, *base, *codeFile, reconnectCopy)
		if e == nil {
			fmt.Fprintln(out, reconnectCopy.Completed)
		}
		return true, e
	case "auto-updates":
		if f.NArg() != 1 || f.Arg(0) != "on" && f.Arg(0) != "off" {
			return true, errors.New("choose auto-updates on or off")
		}
		c, e := config(*base)
		if e != nil {
			return true, e
		}
		guard, e := lock(*base)
		if e != nil {
			return true, e
		}
		defer guard.Close()
		c.Updates.Automatic = f.Arg(0) == "on"
		if e = saveConfig(*base, c); e != nil {
			return true, e
		}
		outcome := "idle"
		if !c.Updates.Automatic {
			outcome = "disabled"
		}
		e = status(*base, agentrelease.UpdateStatus{Outcome: outcome})
		fmt.Fprintf(out, "Local automatic updates: %s\n", f.Arg(0))
		return true, e
	case "uninstall":
		if *keep && *purge {
			return true, errors.New("choose one uninstall option")
		}
		choice := ""
		if *keep {
			choice = "keep"
		}
		if *purge {
			choice = "purge"
		}
		return true, Uninstall(ctx, *base, choice)
	default:
		if f.NArg() != 0 || *archive != "" || *manifest != "" || *signature != "" || *codeFile != "" || *adopt || *purge || *keep {
			return true, errors.New("invalid administrative arguments")
		}
		if mode == "repair" {
			return true, Repair(ctx, *base)
		}
		e := Update(ctx, *base, *automatic, mode == "check")
		if e == nil {
			fmt.Fprintln(out, strings.Title(mode)+" finished. See agent maintenance status in the app.")
		}
		return true, e
	}
}
