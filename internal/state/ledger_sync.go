package state

import (
	"os"
	"path/filepath"
)

func SyncDirectory(store *Store) error {
	dir, err := os.Open(store.Dir())
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func SaveState(store *Store, name string, value any, limit int) error {
	if err := WriteFile(store, name, value, limit); err != nil {
		return err
	}
	return SyncDirectory(store)
}
func ArchiveState(store *Store, name string) error {
	id, err := RandomID()
	if err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(store.Dir(), name), filepath.Join(store.Dir(), name+".abandoned-"+id)); err != nil {
		return err
	}
	return SyncDirectory(store)
}
