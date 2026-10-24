package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

// The extractor is unchanged from signed 0.0.18. Extra systemd members must fit
// its admission/asset-count limits before a future root poll bootstraps the units.
func TestExistingUpdaterAdmitsMaintenanceUnitAssets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root and a secure TMPDIR on the designated disposable host")
	}
	stage := t.TempDir()
	binary := []byte("fixture executable, never executed")
	sum := sha256.Sum256(binary)
	binarySHA := hex.EncodeToString(sum[:])
	runtime := filepath.Join("../../sdk/python/runtime")
	runtimeSHA, e := agentrelease.FileDigest(filepath.Join(runtime, "artifact.json"), 64<<20)
	if e != nil {
		t.Fatal(e)
	}
	descriptor := agentrelease.Descriptor{Format: 1, Version: "0.0.19", SourceCommit: string(bytes.Repeat([]byte("a"), 40)), BinarySHA256: binarySHA, RuntimeSHA256: runtimeSHA}
	raw, _ := json.Marshal(descriptor)
	descriptorSum := sha256.Sum256(raw)
	manifest := agentrelease.Manifest{Version: descriptor.Version, SourceCommit: descriptor.SourceCommit, BinarySHA256: binarySHA, RuntimeSHA256: runtimeSHA, DescriptorSHA256: hex.EncodeToString(descriptorSum[:])}
	members := map[string][]byte{"tinywarden-agent": binary, "platform.json": raw, "LICENSE": []byte("fixture"), "THIRD_PARTY_NOTICES.md": []byte("fixture")}
	if e = filepath.WalkDir(runtime, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(runtime, path)
		if e != nil {
			return e
		}
		content, e := os.ReadFile(path)
		members["runtime/"+filepath.ToSlash(relative)] = content
		return e
	}); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{Service, UpdateService, Timer, "tinywarden-agent-maintenance.service", "tinywarden-agent-maintenance.timer", "tinywarden-agent-maintenance-worker@.service"} {
		content, e := os.ReadFile(filepath.Join("../../deploy/systemd/managed", name))
		if e != nil {
			t.Fatal(e)
		}
		members["systemd/"+name] = content
	}
	if len(members) >= 128 {
		t.Fatal("0.0.18 archive member limit exceeded")
	}
	path := filepath.Join(t.TempDir(), "bundle.tar.gz")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	gzipWriter := gzip.NewWriter(f)
	writer := tar.NewWriter(gzipWriter)
	for name, content := range members {
		if e = writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e = writer.Write(content); e != nil {
			t.Fatal(e)
		}
	}
	if e = writer.Close(); e != nil {
		t.Fatal(e)
	}
	if e = gzipWriter.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(path)
	digest, e := agentrelease.FileDigest(path, 64<<20)
	if e != nil {
		t.Fatal(e)
	}
	manifest.Asset = agentrelease.Asset{Size: info.Size(), SHA256: digest}
	if e = extract(path, stage, manifest); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(stage, "systemd/tinywarden-agent-maintenance-worker@.service")); e != nil {
		t.Fatal(e)
	}
}
