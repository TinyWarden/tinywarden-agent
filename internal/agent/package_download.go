package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	skillruntime "github.com/TinyWarden/tinywarden-agent/internal/skills/runtime"
)

// Download into a private temporary file; credentials never reach the Python child.
func (client *Client) downloadPackage(ctx context.Context, credential string, entry packageAssignment, directory string) (string, error) {
	if !validID(entry.ID) || !contentDigest.MatchString(entry.ArchiveDigest) || entry.ArchiveBytes < 1 || entry.ArchiveBytes > 10<<20 {
		return "", errPackageState
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.Origin+"/api/v2/agent/skill-packages/"+entry.ID, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Accept", "application/zip")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", &ResponseError{Status: response.StatusCode, Code: "package_download_rejected", RetryAfter: retryAfter(response.Header.Get("Retry-After"))}
	}
	if response.Header.Get("Content-Type") != "application/zip" || response.Header.Get("Content-Encoding") != "" ||
		response.Header.Get("X-TinyWarden-Archive-SHA256") != entry.ArchiveDigest || response.Header.Get("X-TinyWarden-Content-SHA256") != entry.Digest ||
		response.ContentLength != -1 && response.ContentLength != entry.ArchiveBytes {
		return "", errPackageState
	}
	file, err := os.CreateTemp(directory, ".download-*.zip")
	if err != nil {
		return "", err
	}
	name := file.Name()
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(name)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, entry.ArchiveBytes+1))
	if err != nil || size != entry.ArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != entry.ArchiveDigest {
		return "", errPackageState
	}
	if file.Sync() != nil {
		return "", errPackageState
	}
	success = true
	return name, nil
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
