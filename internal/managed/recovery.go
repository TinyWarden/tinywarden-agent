package managed

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/TinyWarden/tinywarden-agent/internal/platform"
)

func pendingTransaction(base string) (*transaction, error) {
	b, e := platform.ReadPublic(filepath.Join(base, "updater", "private", "transaction.json"), 32768)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var t transaction
	if e = platform.Decode(b, &t); e != nil {
		return nil, e
	}
	if !validTarget(t.Target) || !validTarget(t.Previous) {
		return nil, errors.New("invalid recovery record")
	}
	switch t.Phase {
	case "completed":
		return nil, nil
	case "staged", "switching", "awaiting_confirmation", "repair_attempted":
		return &t, nil
	default:
		return nil, errors.New("invalid recovery phase")
	}
}

func updateDecision(m platform.Manifest, installed string, p platform.Policy, local, automatic bool) (string, error) {
	n, e := platform.Compare(m.Version, installed)
	if e != nil {
		return "", e
	}
	if n <= 0 {
		return "idle", nil
	}
	if !m.Compatible(installed) {
		return "manual_required", nil
	}
	if !platform.Supports(p.Capabilities, m.Capabilities) {
		return "app_required", nil
	}
	if automatic && (!local || !p.Allowed || !m.Automatic) {
		return "disabled", nil
	}
	return "available", nil
}

func recheck(initial, fresh platform.Policy, local, automatic bool, requestError error) error {
	if requestError != nil || initial.Candidate == nil || fresh.Candidate == nil || *initial.Candidate != *fresh.Candidate || automatic && (!local || !fresh.Allowed) {
		return errors.New("update policy changed")
	}
	return nil
}

func repairVersion(base, target string) error {
	link, e := os.Readlink(filepath.Join(base, "current"))
	if e != nil || filepath.Dir(link) != "releases" {
		return errors.New("invalid installed release pointer")
	}
	n, e := platform.Compare(filepath.Base(link), target)
	if e != nil || n > 0 {
		return errors.New("repair cannot downgrade the active release")
	}
	return nil
}
