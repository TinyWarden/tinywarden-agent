package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func FileDigest(path string, max int64) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	i, err := f.Stat()
	if err != nil || !i.Mode().IsRegular() || i.Size() > max {
		return "", errors.New("identity_invalid")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, max+1))
	if err != nil || n > max {
		return "", errors.New("identity_invalid")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ReleaseDirectory(base string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	if filepath.Base(exe) != "tinywarden-agent" || filepath.Dir(dir) != filepath.Join(base, "releases") || !ValidVersion(filepath.Base(dir)) {
		return "", errors.New("managed_layout_invalid")
	}
	return dir, nil
}

func ReadPublic(path string, limit int64) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return nil, err
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.Mode().IsRegular() || s.Uid != 0 || i.Mode().Perm()&0022 != 0 || i.Size() > limit {
		return nil, errors.New("managed_file_unsafe")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("managed_file_unsafe")
	}
	return b, nil
}

func Identity(base string, managed bool, automatic bool) (Observation, error) {
	o := Observation{Version: Version, Managed: managed, LocalAutomatic: automatic, Updater: UpdateStatus{Outcome: "idle"}}
	if !managed {
		return o, nil
	}
	dir, err := ReleaseDirectory(base)
	if err != nil {
		return o, err
	}
	data, err := ReadPublic(filepath.Join(dir, "platform.json"), 16384)
	if err != nil {
		return o, err
	}
	var d Descriptor
	if err := Decode(data, &d); err != nil || d.Format != 1 || d.Version != Version {
		return o, errors.New("identity_invalid")
	}
	o.DescriptorSHA256 = Digest(data)
	o.BinarySHA256, err = FileDigest(filepath.Join(dir, "tinywarden-agent"), 64<<20)
	if err != nil {
		return o, err
	}
	o.RuntimeSHA256, err = FileDigest(filepath.Join(dir, "runtime", "artifact.json"), 65536)
	if err != nil {
		return o, err
	}
	if o.BinarySHA256 != d.BinarySHA256 || o.RuntimeSHA256 != d.RuntimeSHA256 {
		return o, errors.New("identity_invalid")
	}
	if data, err := ReadPublic(filepath.Join(base, "updater", "status.json"), 2048); err == nil {
		var status UpdateStatus
		if Decode(data, &status) == nil && ValidOutcome(status.Outcome) {
			o.Updater = status
		}
	}
	if data, err := ReadPublic(filepath.Join(base, "updater", "receipt.json"), 32768); err == nil {
		var c Candidate
		if Decode(data, &c) == nil {
			if m, err := Verify([]byte(c.Manifest), c.Signature); err == nil {
				o.InstalledVersion = m.Version
			}
		}
	}
	return o, nil
}
