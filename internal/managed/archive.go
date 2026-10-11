package managed

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/TinyWarden/tinywarden-agent/internal/platform"
	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skills/runtime"
)

func extract(archive, stage string, m platform.Manifest) error {
	digest, err := platform.FileDigest(archive, 64<<20)
	if err != nil || digest != m.Asset.SHA256 {
		return errors.New("archive_digest_invalid")
	}
	i, err := os.Stat(archive)
	if err != nil || i.Size() != m.Asset.Size {
		return errors.New("archive_size_invalid")
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	g, err := gzip.NewReader(f)
	if err != nil {
		return errors.New("archive_invalid")
	}
	defer g.Close()
	t := tar.NewReader(g)
	seen := map[string]bool{}
	total := int64(0)
	for count := 0; ; count++ {
		h, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 128 {
			return errors.New("archive_invalid")
		}
		name := h.Name
		allowed := name == "tinywarden-agent" || name == "platform.json" || name == "LICENSE" || name == "THIRD_PARTY_NOTICES.md" || strings.HasPrefix(name, "runtime/") || strings.HasPrefix(name, "systemd/")
		if !allowed || name != filepath.ToSlash(filepath.Clean(name)) || strings.ContainsAny(name, "\x00\\\r\n") || filepath.IsAbs(name) || strings.HasPrefix(name, "../") || seen[name] || h.Mode&07000 != 0 || h.Size < 0 || h.Size > 64<<20 || h.Typeflag != tar.TypeReg {
			return errors.New("archive_entry_invalid")
		}
		seen[name] = true
		total += h.Size
		if total > 128<<20 {
			return errors.New("archive_too_large")
		}
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err := rootDirectory(filepath.Dir(path), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if name == "tinywarden-agent" {
			mode = 0755
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		if err := out.Chmod(mode); err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, t)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return errors.New("archive_write_failed")
		}
	}
	for _, name := range []string{"tinywarden-agent", "platform.json", "LICENSE", "THIRD_PARTY_NOTICES.md", "systemd/tinywarden-agent.service", "systemd/tinywarden-agent-update.service", "systemd/tinywarden-agent-update.timer"} {
		if !seen[name] {
			return errors.New("archive_incomplete")
		}
	}
	return verifyDirectory(stage, m)
}

func verifyDirectory(dir string, m platform.Manifest) error {
	checks := map[string]string{"tinywarden-agent": m.BinarySHA256, "runtime/artifact.json": m.RuntimeSHA256, "platform.json": m.DescriptorSHA256}
	for path, want := range checks {
		actual, err := platform.FileDigest(filepath.Join(dir, path), 64<<20)
		if err != nil || actual != want {
			return errors.New("release_content_invalid")
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "platform.json"))
	if err != nil {
		return err
	}
	var d platform.Descriptor
	if platform.Decode(b, &d) != nil || d.Format != 1 || d.Version != m.Version || d.SourceCommit != m.SourceCommit || d.BinarySHA256 != m.BinarySHA256 || d.RuntimeSHA256 != m.RuntimeSHA256 {
		return errors.New("descriptor_invalid")
	}
	if err := (skillruntime.Client{Assets: filepath.Join(dir, "runtime")}).VerifyAssets(); err != nil {
		return errors.New("runtime_content_invalid")
	}
	return syncDirectory(dir)
}
