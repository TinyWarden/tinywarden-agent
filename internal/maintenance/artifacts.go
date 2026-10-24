package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
)

func ArtifactPath(base, job, kind string) (string, error) {
	if !uuidPattern.MatchString(job) || !regexp.MustCompile(`^[a-z][a-z_]{1,31}$`).MatchString(kind) {
		return "", errors.New("maintenance_scope_invalid")
	}
	return filepath.Join(base, "updater", "private", "maintenance-"+job+"-"+kind+".json"), nil
}

// Empty durable file precedes spawn. Native output owns a regular descriptor,
// never a pipe whose reader lifetime could interrupt a package maintainer script.
func CreateArtifact(base, job, kind string) (*os.File, error) {
	path, e := ArtifactPath(base, job, kind)
	if e != nil {
		return nil, e
	}
	if e = hostmutation.PrivateWrite(path, nil); e != nil {
		return nil, e
	}
	fd, e := syscall.Open(path, syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || i.Sys().(*syscall.Stat_t).Uid != 0 {
		f.Close()
		return nil, errors.New("maintenance_journal_invalid")
	}
	return f, nil
}
func ReadArtifact(base, job, kind string, limit int) ([]byte, error) {
	path, e := ArtifactPath(base, job, kind)
	if e != nil {
		return nil, e
	}
	return readPrivate(path, limit)
}
