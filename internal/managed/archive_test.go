package managed

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/platform"
)

func maliciousArchive(t *testing.T, h *tar.Header) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.tar.gz")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	if e = w.WriteHeader(h); e != nil {
		t.Fatal(e)
	}
	if h.Size > 0 {
		if _, e = w.Write([]byte(strings.Repeat("a", int(h.Size)))); e != nil {
			t.Fatal(e)
		}
	}
	_ = w.Close()
	_ = gz.Close()
	_ = f.Close()
	return path
}
func TestExtractionRejectsUnsafeMembers(t *testing.T) {
	for _, h := range []*tar.Header{
		{Name: "../escape", Mode: 0644, Typeflag: tar.TypeReg, Size: 1},
		{Name: "/tmp/escape", Mode: 0644, Typeflag: tar.TypeReg, Size: 1},
		{Name: "runtime/link", Mode: 0644, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "runtime/device", Mode: 0644, Typeflag: tar.TypeChar},
		{Name: "runtime/elevated", Mode: 04755, Typeflag: tar.TypeReg, Size: 1},
		{Name: "unexpected", Mode: 0644, Typeflag: tar.TypeReg, Size: 1},
	} {
		t.Run(h.Name, func(t *testing.T) {
			path := maliciousArchive(t, h)
			i, _ := os.Stat(path)
			hash, _ := platform.FileDigest(path, 64<<20)
			m := platform.Manifest{Asset: platform.Asset{Size: i.Size(), SHA256: hash}}
			if e := extract(path, t.TempDir(), m); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestArchiveDigestBeforeExtraction(t *testing.T) {
	path := maliciousArchive(t, &tar.Header{Name: "tinywarden-agent", Mode: 0755, Typeflag: tar.TypeReg, Size: 1})
	i, _ := os.Stat(path)
	stage := t.TempDir()
	if e := extract(path, stage, platform.Manifest{Asset: platform.Asset{Size: i.Size(), SHA256: strings.Repeat("0", 64)}}); e == nil {
		t.Fatal("bad hash accepted")
	}
	entries, _ := os.ReadDir(stage)
	if len(entries) != 0 {
		t.Fatal("extracted before verifying")
	}
}
func TestBaseAndUnitEscaping(t *testing.T) {
	for _, path := range []string{"/", "/opt", "relative", "/proc/example", "/opt/../opt/app", "/opt/app\n[Service]"} {
		if basePath(path) == nil {
			t.Fatal(path)
		}
	}
	if got := unitQuote(`/opt/a "quote" 100%`); got != `/opt/a \"quote\" 100%%` {
		t.Fatal(got)
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	_ = os.Symlink("/opt", link)
	if safePath(filepath.Join(link, "agent"), true) == nil {
		t.Fatal("symlink ancestry allowed")
	}
}
