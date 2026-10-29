package skillactions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const actionLogBytes = 128 * 1024

// Diagnostic storage is job-scoped and bounded; it is never a receipt or authority.
func (p *Provider) logEntries() []json.RawMessage {
	path := filepath.Join(p.Loader.Base, "updater", "private", "action-state-"+p.Intent.ID, "operation-log.jsonl")
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > actionLogBytes {
		return nil
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Nlink != 1 || owner.Uid != uint32(os.Geteuid()) && owner.Uid != 65534 {
		return nil
	}
	raw := make([]byte, actionLogBytes+1)
	n, _ := file.Read(raw)
	if n > actionLogBytes {
		return nil
	}
	last := bytes.LastIndexByte(raw[:n], '\n')
	if last < 0 {
		return nil
	}
	entries := make([]json.RawMessage, 0, 128)
	scanner := bufio.NewScanner(bytes.NewReader(raw[:last+1]))
	for scanner.Scan() && len(entries) < 128 {
		line := scanner.Bytes()
		var record struct {
			Sequence int `json:"sequence"`
		}
		if len(line) > 1023 || !json.Valid(line) || json.Unmarshal(line, &record) != nil || record.Sequence != len(entries)+1 {
			break
		}
		entries = append(entries, append(json.RawMessage(nil), line...))
	}
	return entries
}

// Logs use an independent bounded request, never the action event sequence.
// Delivery failure cannot cancel or change the native operation.
func (p *Provider) watchLogs() func() {
	if p.Loader.Log == nil {
		return func() {}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		sent := 0
		flush := func() {
			entries := p.logEntries()
			if len(entries) <= sent {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			err := p.Loader.Log(ctx, p.Intent, entries[sent:])
			cancel()
			if err == nil {
				sent = len(entries)
			}
		}
		ticker := time.NewTicker(750 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				flush()
			case <-stop:
				flush()
				return
			}
		}
	}()
	return func() { close(stop); <-done }
}
