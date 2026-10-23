package state

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func ReadFile(store *Store, name string, max int, target any) (bool, error) {
	path := filepath.Join(store.Dir(), name)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		stat.Uid != uint32(os.Geteuid()) || info.Size() > int64(max) {
		return false, errors.New("unsafe disk state")
	}
	decoder := json.NewDecoder(io.LimitReader(file, int64(max)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return false, errors.New("trailing disk state")
	}
	return true, nil
}

func WriteFile(store *Store, name string, value any, max int) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > max {
		return errors.New("disk state too large")
	}
	file, err := os.CreateTemp(store.Dir(), ".disk-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(store.Dir(), name)); err != nil {
		return err
	}
	return store.Sync()
}
