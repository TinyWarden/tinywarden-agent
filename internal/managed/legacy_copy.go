package managed

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func copyTree(source, target string, uid, gid int) error {
	count := 0
	var total int64
	return filepath.WalkDir(source, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 10000 {
			return errors.New("legacy data exceeds migration limit")
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		destination := filepath.Join(target, rel)
		if d.IsDir() {
			if e = os.Mkdir(destination, 0700); e != nil && !os.IsExist(e) {
				return e
			}
			return os.Chown(destination, uid, gid)
		}
		n, e := copyLegacyFile(path, destination, 128<<20-total)
		total += n
		return e
	})
}

func copyLegacyFile(source, destination string, remaining int64) (int64, error) {
	fd, e := syscall.Open(source, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return 0, e
	}
	input := os.NewFile(uintptr(fd), source)
	defer input.Close()
	i, e := input.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Size() > 64<<20 || i.Size() > remaining {
		return 0, errors.New("unsupported or oversized legacy data")
	}
	if strings.HasSuffix(source, ".json") && i.Size() <= 1<<20 {
		b, e := io.ReadAll(io.LimitReader(input, (1<<20)+1))
		if e != nil || len(b) > 1<<20 {
			return 0, errors.New("legacy JSON exceeds migration limit")
		}
		if bytes.Contains(b, []byte(legacyData)) || bytes.Contains(b, []byte("/usr/local/lib/tinywarden-agent")) {
			return 0, errors.New("legacy data has absolute layout references requiring manual migration")
		}
		if _, e = input.Seek(0, 0); e != nil {
			return 0, e
		}
	}
	output, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return 0, e
	}
	defer output.Close()
	limit := min(remaining, 64<<20)
	n, e := io.Copy(output, io.LimitReader(input, limit+1))
	if e != nil || n > limit {
		return n, errors.New("legacy data exceeds migration limit")
	}
	if e = output.Sync(); e != nil {
		return n, e
	}
	return n, output.Close()
}

func RemoveLegacyData() error {
	if os.Geteuid() == 0 {
		return errors.New("retired data cleanup must run as the agent")
	}
	// Immutable skill caches are owned by the agent but have read-only directories.
	if e := filepath.WalkDir(legacyData, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.Chmod(path, 0700)
		}
		return nil
	}); e != nil {
		return e
	}
	entries, e := os.ReadDir(legacyData)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e = os.RemoveAll(filepath.Join(legacyData, entry.Name())); e != nil {
			return e
		}
	}
	return nil
}
