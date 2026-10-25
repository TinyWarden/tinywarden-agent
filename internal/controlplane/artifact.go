package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
)

// FetchArtifact shares the fixed-origin, bounded ZIP transport used by monitoring
// and action delivery. The credential never reaches a package process.
func (client *Client) FetchArtifact(ctx context.Context, credential, path, content, directory string, archive actioncontract.Archive) (string, error) {
	if !archive.Valid() || !digestArtifact(content) || !strings.HasPrefix(path, "/api/") || strings.ContainsAny(path, "?#\\\r\n\t ") {
		return "", errors.New("package_download_invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.Origin+path, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Accept", "application/zip")
	httpClient := *client.HTTP
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", &ResponseError{Status: response.StatusCode, Code: "package_download_rejected", RetryAfter: RetryAfter(response.Header.Get("Retry-After"))}
	}
	if response.Header.Get("Content-Type") != "application/zip" || response.Header.Get("Content-Encoding") != "" || response.Header.Get("X-TinyWarden-Archive-SHA256") != archive.SHA256 || response.Header.Get("X-TinyWarden-Content-SHA256") != content || response.ContentLength != -1 && response.ContentLength != archive.Size {
		return "", errors.New("package_download_invalid")
	}
	file, err := os.CreateTemp(directory, ".download-*.zip")
	if err != nil {
		return "", err
	}
	name, success := file.Name(), false
	defer func() {
		file.Close()
		if !success {
			os.Remove(name)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, archive.Size+1))
	if err != nil || size != archive.Size || hex.EncodeToString(hash.Sum(nil)) != archive.SHA256 || file.Sync() != nil {
		return "", errors.New("package_download_invalid")
	}
	success = true
	return name, nil
}

func digestArtifact(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
