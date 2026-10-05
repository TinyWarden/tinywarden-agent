package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPackageDownloadPinsScopeBytesAndRejectsRedirects(t *testing.T) {
	data := []byte("synthetic container")
	sum := sha256.Sum256(data)
	entry := packageAssignment{ID: "11111111-1111-4111-8111-111111111111", Digest: strings.Repeat("a", 64), ArchiveDigest: hex.EncodeToString(sum[:]), ArchiveBytes: int64(len(data))}
	for _, mode := range []string{"ok", "changed", "oversized", "truncated", "encoded", "redirect", "stale"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v2/agent/skill-packages/"+entry.ID || r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
					t.Error("incorrect credential scope")
				}
				if mode == "redirect" {
					w.Header().Set("Location", "https://example.invalid")
					w.WriteHeader(302)
					return
				}
				if mode == "stale" {
					w.WriteHeader(409)
					return
				}
				w.Header().Set("Content-Type", "application/zip")
				w.Header().Set("X-TinyWarden-Archive-SHA256", entry.ArchiveDigest)
				w.Header().Set("X-TinyWarden-Content-SHA256", entry.Digest)
				if mode == "encoded" {
					w.Header().Set("Content-Encoding", "gzip")
				}
				body := data
				switch mode {
				case "changed":
					body = []byte("altered container!!")
				case "oversized":
					body = append(append([]byte{}, data...), 0)
				case "truncated":
					body = data[:len(data)-1]
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			folder := t.TempDir()
			name, err := NewClient(server.URL).downloadPackage(context.Background(), "synthetic-token", entry, folder)
			if mode == "ok" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(name)
				if string(got) != string(data) {
					t.Fatal("bytes changed")
				}
				info, _ := os.Stat(name)
				if info.Mode().Perm() != 0o600 {
					t.Fatal("download permissions")
				}
			} else {
				if err == nil {
					t.Fatal("unsafe download accepted")
				}
				files, _ := os.ReadDir(folder)
				if len(files) != 0 {
					t.Fatal("failed transfer retained")
				}
			}
		})
	}
}
