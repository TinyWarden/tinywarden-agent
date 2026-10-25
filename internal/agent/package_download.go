package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
	agentstate "github.com/TinyWarden/tinywarden-agent/internal/state"
)

// Download into a private temporary file; credentials never reach the Python child.
func (client *Client) downloadPackage(ctx context.Context, credential string, entry packageAssignment, directory string) (string, error) {
	if !agentstate.ValidID(entry.ID) {
		return "", errPackageState
	}
	return client.FetchArtifact(ctx, credential, "/api/v2/agent/skill-packages/"+entry.ID, entry.Digest, directory,
		actioncontract.Archive{SHA256: entry.ArchiveDigest, Size: entry.ArchiveBytes})
}

func (lane *packageLane) ensurePackage(ctx context.Context, entry packageAssignment) (string, error) {
	store := filepath.Join(lane.config.StateDir, "skills")
	if err := os.MkdirAll(store, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(store)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", errPackageState
	}
	directory := filepath.Join(store, entry.Digest)
	info, err = os.Lstat(directory)
	if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return directory, nil
	}
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", errPackageState
	}
	archive, err := lane.client.downloadPackage(ctx, lane.state.Credential, entry, store)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	output, err := lane.runtime.Invoke(ctx, skillruntime.Request{Action: "publish_archive", Store: store, Archive: archive,
		ArchiveSHA256: entry.ArchiveDigest, ContentSHA256: entry.Digest, Official: entry.Official})
	if err != nil {
		return "", err
	}
	var result struct {
		ContentSHA256 string `json:"content_sha256"`
	}
	if json.Unmarshal(output, &result) != nil || result.ContentSHA256 != entry.Digest {
		return "", errPackageState
	}
	return directory, nil
}
