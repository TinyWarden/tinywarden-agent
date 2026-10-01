package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func readBaselineState(store *Store, name string, limit int, target any) (bool, error) {
	var raw json.RawMessage
	found, err := readStateFile(store, name, limit, &raw)
	if err != nil || !found {
		return found, err
	}
	if len(raw) > limit || decodeBaseline(raw, target) != nil {
		return true, errBaselineState
	}
	return true, nil
}
func syncBaselineDir(store *Store) error {
	dir, err := os.Open(store.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func saveBaselineState(store *Store, name string, value any, limit int) error {
	if err := saveStateFile(store, name, value, limit); err != nil {
		return err
	}
	return syncBaselineDir(store)
}
func archiveBaselineState(store *Store, name string) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(store.dir, name), filepath.Join(store.dir, name+".abandoned-"+id)); err != nil {
		return err
	}
	return syncBaselineDir(store)
}

func loadBaselineCache(store *Store, scope baselineScope) (*baselineCache, error) {
	var cache baselineCache
	found, err := readBaselineState(store, "baseline-assignments.json", baselineBytes, &cache)
	if err != nil || !found {
		return nil, err
	}
	if !validBaselineCache(cache) {
		return nil, errBaselineState
	}
	if cache.Scope != scope {
		return nil, archiveBaselineState(store, "baseline-assignments.json")
	}
	return &cache, nil
}
func saveBaselineCache(store *Store, cache baselineCache) error {
	if !validBaselineCache(cache) {
		return errBaselineState
	}
	return saveBaselineState(store, "baseline-assignments.json", cache, baselineBytes)
}
func baselineLease(cache *baselineCache, now time.Time) (time.Time, bool) {
	if cache == nil || cache.Paused || !validInstant(cache.ValidatedAt) {
		return time.Time{}, false
	}
	validated, _ := time.Parse("2006-01-02T15:04:05.000Z", cache.ValidatedAt)
	expires := validated.Add(24 * time.Hour)
	return expires, !now.Before(validated) && now.Before(expires)
}

func pauseBaseline(store *Store, cache **baselineCache) error {
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
