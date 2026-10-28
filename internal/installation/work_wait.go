package installation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

var workCursorPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// A cursor is only a wake-up hint; ordinary discovery still owns all authority.
func waitForWork(ctx context.Context, base string) error {
	cfg, err := config(base)
	if err != nil {
		return err
	}
	state, err := identity(base, cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(base, "updater", "private", "work-wait.json")
	client := controlplane.NewClient(cfg.ControlPlaneOrigin)
	transport := client.HTTP.Transport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	defer transport.CloseIdleConnections()
	client.HTTP.Transport = transport
	client.HTTP.Timeout = 35 * time.Second
	return waitForWorkHint(ctx, client, state.Credential, path)
}

// Cache and transport failures only delay the ordinary authenticated poll.
func waitForWorkHint(ctx context.Context, client *controlplane.Client, credential, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cursor, err := loadWorkCursor(path)
	if err != nil {
		return workWaitBackoff(ctx)
	}
	next, err := receiveWorkHint(ctx, client, credential, cursor)
	if err != nil || next == "" {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if atomicFile(path, next, 0600) != nil {
		return workWaitBackoff(ctx)
	}
	return nil
}

func loadWorkCursor(path string) (*string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Sys().(*syscall.Stat_t).Nlink != 1 || info.Size() > 128 {
		return nil, errors.New("invalid_work_cursor")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil {
		return nil, err
	}
	var cursor string
	if controlplane.Decode(raw, &cursor) != nil || !workCursorPattern.MatchString(cursor) {
		return nil, errors.New("invalid_work_cursor")
	}
	return &cursor, nil
}

func receiveWorkHint(ctx context.Context, client *controlplane.Client, credential string, cursor *string) (string, error) {
	var reply struct {
		Schema    int    `json:"schema_version"`
		Available *bool  `json:"available"`
		Cursor    string `json:"cursor"`
	}
	_, err := client.Post(ctx, "/api/v2/agent/work-wait", credential, map[string]any{"schema_version": 1, "wait_seconds": 25, "cursor": cursor}, false, func(raw []byte) error {
		if e := controlplane.Decode(raw, &reply); e != nil {
			return e
		}
		if reply.Schema != 1 || reply.Available == nil || !workCursorPattern.MatchString(reply.Cursor) {
			return errors.New("invalid_work_reply")
		}
		return nil
	}, 1024, 1024)
	if err == nil {
		return reply.Cursor, nil
	}
	// This optional lane never prevents discovery/recovery through the job API.
	return "", workWaitBackoff(ctx)
}

func workWaitBackoff(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return ctx.Err()
	}
}
