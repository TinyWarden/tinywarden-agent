package skillactions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

// InspectAction retains provider-native locks until the local journal callback
// has committed. A regular receipt precedes the pipe notification and ack.
func (p *Provider) InspectAction(parent context.Context, commit func(json.RawMessage) error) error {
	if commit == nil {
		return errors.New("maintenance_guard_required")
	}
	request, err := p.request("inspect", nil, nil, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	boot, started, err := maintenance.BootClock()
	if err != nil {
		return err
	}
	timely := func() bool {
		current, now, e := maintenance.BootClock()
		return e == nil && boot == current && now >= started && now-started < 90000 && ctx.Err() == nil
	}
	output, err := maintenance.CreateArtifact(p.Loader.Base, p.Intent.ID, "action_inspect")
	if err != nil {
		return err
	}
	defer output.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	cmd, err := p.command(ctx, request, output, writer)
	if err != nil {
		return err
	}
	cmd.Stdin = nil
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		return err
	}
	_ = writer.Close()
	interrupt := func() error {
		input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("maintenance_inspection_interrupted")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return interrupt()
	}
	if _, err = input.Write(append(raw, '\n')); err != nil {
		return interrupt()
	}
	type notification struct {
		value byte
		err   error
	}
	ready := make(chan notification, 1)
	go func() { data := make([]byte, 1); _, e := reader.Read(data); ready <- notification{data[0], e} }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case value := <-ready:
			if value.err != nil || value.value != '\n' {
				return interrupt()
			}
			break waiting
		case <-ctx.Done():
			return interrupt()
		case <-ticker.C:
			if !timely() {
				return interrupt()
			}
		}
	}
	raw, err = p.read(request)
	if err != nil {
		return interrupt()
	}
	var value struct {
		Facts json.RawMessage `json:"facts"`
	}
	if len(raw) > maintenance.MaxControl || controlplane.DecodeArtifact(raw, &value, 8192) != nil || !timely() {
		return interrupt()
	}
	if err = p.Loader.Verify(); err != nil || !timely() {
		return interrupt()
	}
	// No timeout kills this lock holder during the local-only atomic callback.
	err = commit(value.Facts)
	if err == nil {
		_, err = input.Write([]byte{'\n'})
	}
	_ = input.Close()
	wait := cmd.Wait()
	if err != nil {
		return err
	}
	if wait != nil {
		return errors.New("maintenance_inspection_interrupted")
	}
	return nil
}
