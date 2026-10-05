// Package runtime supervises the trusted Python launcher, never imports skill code.
package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const Capability = "skill_packages.python313.v1"
const MaxJSON = 1 << 20

type Request struct {
	Action           string            `json:"action"`
	Package          string            `json:"package"`
	Store            string            `json:"store,omitempty"`
	Archive          string            `json:"archive,omitempty"`
	ArchiveSHA256    string            `json:"archive_sha256,omitempty"`
	ContentSHA256    string            `json:"content_sha256,omitempty"`
	Official         bool              `json:"official,omitempty"`
	Function         string            `json:"function,omitempty"`
	Arguments        json.RawMessage   `json:"arguments,omitempty"`
	Grants           []json.RawMessage `json:"grants,omitempty"`
	Ceiling          []json.RawMessage `json:"ceiling,omitempty"`
	FilesystemHelper string            `json:"filesystem_helper,omitempty"`
	ProtectedPaths   []string          `json:"protected_paths,omitempty"`
}
type Response struct {
	Result        json.RawMessage `json:"result,omitempty"`
	ContentSHA256 string          `json:"content_sha256,omitempty"`
	Error         string          `json:"error,omitempty"`
}
type Client struct{ Assets string }
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

type boundedWriter struct {
	bytes.Buffer
	limit  int
	cancel func()
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if w.Len()+len(data) > w.limit {
		w.cancel()
		return 0, errors.New("output_exceeded")
	}
	return w.Buffer.Write(data)
}

func (c Client) VerifyAssets() error {
	if !filepath.IsAbs(c.Assets) || filepath.Clean(c.Assets) != c.Assets {
		return &Error{"runtime_unavailable"}
	}
	data, err := os.ReadFile(filepath.Join(c.Assets, "artifact.json"))
	if err != nil || len(data) > 65536 {
		return &Error{"runtime_unavailable"}
	}
	var manifest struct {
		Format  int               `json:"format"`
		SDK     int               `json:"sdk"`
		Runtime string            `json:"runtime"`
		Files   map[string]string `json:"files"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Format != 1 || manifest.SDK != 1 || manifest.Runtime != "python-3.13-v1" || len(manifest.Files) > 64 || len(manifest.Files) < 10 || manifest.Files["supervisor.py"] == "" || manifest.Files["seccomp.py"] == "" {
		return &Error{"runtime_unavailable"}
	}
	for name, digest := range manifest.Files {
		if !regexp.MustCompile(`^[a-zA-Z0-9_.\/-]+$`).MatchString(name) || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "../") {
			return &Error{"runtime_unavailable"}
		}
		path := filepath.Join(c.Assets, name)
		for directory := filepath.Dir(path); directory != filepath.Dir(c.Assets); directory = filepath.Dir(directory) {
			info, err := os.Lstat(directory)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return &Error{"runtime_unavailable"}
			}
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxJSON {
			return &Error{"runtime_unavailable"}
		}
		contents, err := os.ReadFile(path)
		sum := sha256.Sum256(contents)
		if err != nil || hex.EncodeToString(sum[:]) != digest {
			return &Error{"runtime_unavailable"}
		}
	}
	return nil
}

func (c Client) Invoke(ctx context.Context, request Request) ([]byte, error) {
	if c.VerifyAssets() != nil {
		return nil, &Error{"runtime_unavailable"}
	}
	input, err := json.Marshal(request)
	if err != nil || len(input) > MaxJSON {
		return nil, &Error{"input_exceeded"}
	}
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3.13", "-I", "-S", "-B", filepath.Join(c.Assets, "supervisor.py"))
	command.Env, command.Dir = []string{}, "/"
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 3 * time.Second
	command.Stdin = bytes.NewReader(input)
	out, diagnostics := &boundedWriter{limit: MaxJSON, cancel: cancel}, &boundedWriter{limit: 16384, cancel: cancel}
	command.Stdout, command.Stderr = out, diagnostics
	if err := command.Run(); err != nil {
		if command.Process == nil {
			return nil, &Error{"runtime_unavailable"}
		}
		return nil, &Error{"cleanup_failed"}
	}
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(out.Bytes(), &response) != nil {
		return nil, &Error{"output_invalid"}
	}
	if response.Error != "" {
		return nil, &Error{response.Error}
	}
	return append([]byte(nil), out.Bytes()...), nil
}

func (c Client) Collect(ctx context.Context, request Request) (json.RawMessage, error) {
	request.Action, request.Function = "run", "collect"
	output, err := c.Invoke(ctx, request)
	if err != nil {
		return nil, err
	}
	var response Response
	if json.Unmarshal(output, &response) != nil || response.ContentSHA256 != request.ContentSHA256 || len(response.Result) == 0 {
		return nil, &Error{"output_invalid"}
	}
	return response.Result, nil
}

func (c Client) Ready(ctx context.Context) bool {
	data, err := os.ReadFile(filepath.Join(c.Assets, "artifact.json"))
	var artifact struct {
		ProbeSHA256 string `json:"probe_sha256"`
	}
	if err != nil || json.Unmarshal(data, &artifact) != nil || len(artifact.ProbeSHA256) != 64 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := c.Invoke(ctx, Request{Action: "run", Package: filepath.Join(c.Assets, "probe"),
		ContentSHA256: artifact.ProbeSHA256, Function: "validate_settings", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		return false
	}
	var response Response
	return json.Unmarshal(out, &response) == nil && string(response.Result) == "[]" && response.ContentSHA256 == artifact.ProbeSHA256
}

var _ io.Writer = (*boundedWriter)(nil)
