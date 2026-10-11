package platform

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func HTTP(download bool) *http.Client {
	timeout := 10 * time.Second
	if download {
		timeout = 90 * time.Second
	}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil, DisableCompression: true,
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !download || len(via) > 3 || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" ||
				(req.URL.Hostname() != "release-assets.githubusercontent.com" && req.URL.Hostname() != "objects.githubusercontent.com") {
				return http.ErrUseLastResponse
			}
			req.Header.Del("Authorization")
			return nil
		}}
}

func Post(ctx context.Context, client *http.Client, origin, path, credential string, input, out any, limit int64) error {
	if _, err := Origin(origin); err != nil {
		return err
	}
	data, err := json.Marshal(input)
	if err != nil || len(data) > 8192 {
		return errors.New("invalid_request")
	}
	r, err := http.NewRequestWithContext(ctx, "POST", origin+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.Do(r)
	if err != nil {
		return errors.New("app_unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("app_status_%d", response.StatusCode)
	}
	if response.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return errors.New("invalid_response")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return errors.New("invalid_response")
	}
	return Decode(body, out)
}

func Download(ctx context.Context, client *http.Client, address, path string, size int64, digest string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" ||
		!strings.HasPrefix(u.Path, "/"+Repository+"/releases/download/v") || size < 1 || size > 64<<20 || !ValidDigest(digest) {
		return errors.New("invalid_download")
	}
	r, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(r)
	if err != nil {
		return errors.New("download_failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "" {
		return errors.New("download_failed")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	n, err := io.Copy(file, io.LimitReader(response.Body, size+1))
	if err != nil || n != size {
		return errors.New("download_size_invalid")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	actual, err := FileDigest(path, 64<<20)
	if err != nil || actual != digest {
		return errors.New("download_digest_invalid")
	}
	ok = true
	return nil
}
