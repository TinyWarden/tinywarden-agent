package managed

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Uninstall(ctx context.Context, base, choice string) error {
	if os.Geteuid() != 0 {
		return errors.New("uninstall requires root")
	}
	fmt.Println("Server records and history in the app will be retained.")
	if e := basePath(base); e != nil {
		return e
	}
	if _, e := config(base); e != nil {
		return e
	}
	guard, e := lock(base)
	if e != nil {
		return e
	}
	defer guard.Close()
	if choice == "" {
		tty, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if e != nil {
			return errors.New("choose --keep-data or --purge explicitly")
		}
		defer tty.Close()
		fmt.Fprintf(tty, "Server records and history in the app will be retained in both cases.\n1. Stop and disable services; keep installation and data\n2. Purge this local installation (%s)\nChoose 1 or 2: ", base)
		line, e := bufio.NewReader(tty).ReadString('\n')
		if e != nil {
			return e
		}
		switch strings.TrimSpace(line) {
		case "1":
			choice = "keep"
		case "2":
			choice = "purge"
		default:
			return errors.New("uninstall cancelled")
		}
	}
	if choice != "keep" && choice != "purge" {
		return errors.New("invalid uninstall choice")
	}
	// Inspect every registration before stopping or removing anything.
	for _, name := range []string{Service, UpdateService, Timer} {
		path := filepath.Join("/etc/systemd/system", name)
		target, e := os.Readlink(path)
		if e != nil || target != filepath.Join(base, "updater", "systemd", name) {
			return errors.New("unrecognized service registration: " + path)
		}
	}
	if e := system(ctx, "disable", "--now", Timer); e != nil {
		return e
	}
	if e := system(ctx, "stop", UpdateService); e != nil {
		return e
	}
	if e := system(ctx, "disable", "--now", Service); e != nil {
		return e
	}
	if choice == "keep" {
		fmt.Println("Services stopped and disabled. Installation and enrolled data kept. Server records and history in the app are unchanged.")
		return nil
	}
	for _, name := range []string{Service, UpdateService, Timer} {
		if e := os.Remove(filepath.Join("/etc/systemd/system", name)); e != nil {
			return e
		}
	}
	if e := system(ctx, "daemon-reload"); e != nil {
		return e
	}
	if e := os.RemoveAll(base); e != nil {
		return e
	}
	fmt.Println("Local installation purged. Server records and history in the app are unchanged.")
	return nil
}
