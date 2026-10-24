package apt

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
)

// Inspect holds native package locks until the local-only callback has committed.
// The bounded pipe is safe here: this helper can only read package state.
func (p *Provider) Inspect(parent context.Context, commit func(map[string]string) error) error {
	if p.Guard == nil || commit == nil {
		return errors.New("maintenance_guard_required")
	}
	if p.Verify != nil {
		if e := p.Verify(); e != nil {
			return e
		}
	}
	source, e := Source()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	boot, started, e := maintenance.BootClock()
	if e != nil {
		return e
	}
	timely := func() bool {
		currentBoot, now, err := maintenance.BootClock()
		return err == nil && currentBoot == boot && now >= started && now-started < 90000 && ctx.Err() == nil
	}
	cmd := exec.Command("/usr/bin/python3", "-I", "-B", "-c", "MAINTENANCE_INSPECT = True\n"+source)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC"}
	cmd.ExtraFiles = []*os.File{p.Guard}
	input, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		input.Close()
		return e
	}
	if e = cmd.Start(); e != nil {
		input.Close()
		output.Close()
		return e
	}
	type reading struct {
		raw []byte
		err error
	}
	ready := make(chan reading, 1)
	go func() {
		raw, e := bufio.NewReaderSize(output, maintenance.MaxControl+1).ReadSlice('\n')
		ready <- reading{raw, e}
	}()
	var received reading
	interrupt := func() error {
		input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("maintenance_inspection_interrupted")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case received = <-ready:
			break waiting
		case <-ctx.Done():
			return interrupt()
		case <-ticker.C:
			if !timely() {
				return interrupt()
			}
		}
	}
	raw, readErr := received.raw, received.err
	var proof struct {
		Schema int               `json:"schema_version"`
		Facts  map[string]string `json:"facts"`
	}
	if readErr != nil || len(raw) > maintenance.MaxControl || controlplane.Decode(raw, &proof) != nil || proof.Schema != 1 {
		input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("maintenance_reconciliation_required")
	}
	if !timely() {
		return interrupt()
	}
	// No timer may kill the lock holder during the local commit callback.
	if p.Verify != nil {
		e = p.Verify()
	}
	if !timely() {
		return interrupt()
	}
	if e == nil {
		e = commit(proof.Facts)
	}
	_ = input.Close() // Only releases a read-only inspector; never signals a package process.
	waitErr := cmd.Wait()
	if e != nil {
		return e
	}
	if waitErr != nil {
		return errors.New("maintenance_inspection_interrupted")
	}
	return nil
}
