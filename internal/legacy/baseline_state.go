package legacy

import (
	"time"

	controlplane "github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

func loadBaselineCache(store *agentstate.Store, scope agentstate.Scope) (*baselineCache, error) {
	var cache baselineCache
	found, err := controlplane.ReadState(store, "baseline-assignments.json", baselineBytes, &cache)
	if err != nil || !found {
		return nil, err
	}
	if !validBaselineCache(cache) {
		return nil, controlplane.ErrInvalidState
	}
	if cache.Scope != scope {
		return nil, agentstate.ArchiveState(store, "baseline-assignments.json")
	}
	return &cache, nil
}
func saveBaselineCache(store *agentstate.Store, cache baselineCache) error {
	if !validBaselineCache(cache) {
		return controlplane.ErrInvalidState
	}
	return agentstate.SaveState(store, "baseline-assignments.json", cache, baselineBytes)
}
func baselineLease(cache *baselineCache, now time.Time) (time.Time, bool) {
	if cache == nil || cache.Paused || !agentstate.ValidInstant(cache.ValidatedAt) {
		return time.Time{}, false
	}
	validated, _ := time.Parse("2006-01-02T15:04:05.000Z", cache.ValidatedAt)
	expires := validated.Add(24 * time.Hour)
	return expires, !now.Before(validated) && now.Before(expires)
}

func pauseBaseline(store *agentstate.Store, cache **baselineCache) error {
	if *cache == nil {
		return nil
	}
	copy := **cache
	copy.Paused = true
	if err := saveBaselineCache(store, copy); err != nil {
		return err
	}
	*cache = &copy
	return nil
}
