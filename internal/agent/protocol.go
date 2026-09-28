package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

const Version = "0.0.1"
const maxBody = 16 * 1024

type Client struct {
	Origin string
	HTTP   *http.Client
}

func NewClient(origin string) *Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableCompression:    true,
	}
	return &Client{Origin: origin, HTTP: &http.Client{Transport: transport,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type ResponseError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

// The server may have committed a request before its response body was interrupted.
// Keep the original pending request for a bounded retry without leaking the raw URL.
type responseReadError struct{ cause error }

func (err *responseReadError) Error() string { return "response interrupted" }
func (err *responseReadError) Unwrap() error { return err.cause }

func retryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds := 0
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0
		}
		if seconds < 900 {
			seconds = seconds*10 + int(digit-'0')
			if seconds > 900 {
				seconds = 900
			}
		}
	}
	return time.Duration(seconds) * time.Second
}

func (err *ResponseError) Error() string {
	return fmt.Sprintf("server status %d (%s)", err.Status, err.Code)
}

func (client *Client) post(ctx context.Context, path, credential string, input any, allowCreated bool) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(input)
	if err != nil || len(data) > maxBody {
		return nil, errors.New("invalid request body")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.Origin+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Accept", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, errors.New("redirect refused")
	}
	delay := retryAfter(response.Header.Get("Retry-After"))
	// Response status remains authoritative when an error body is interrupted.
	// Closing an unread body bounds resource use and prevents retrying a rejection.
	if response.StatusCode == 429 || response.StatusCode >= 500 {
		return nil, &ResponseError{Status: response.StatusCode, Code: "temporarily_unavailable", RetryAfter: delay}
	}
	if response.StatusCode >= 400 {
		return nil, &ResponseError{Status: response.StatusCode, Code: "request_rejected"}
	}
	if response.StatusCode == 201 && !allowCreated {
		return nil, errors.New("unexpected created response")
	}
	if response.StatusCode != 200 && response.StatusCode != 201 {
		return nil, errors.New("unexpected response status")
	}
	if response.Header.Get("Content-Encoding") != "" {
		return nil, errors.New("encoded response refused")
	}
	media, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" ||
		(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil, errors.New("invalid response media")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if len(body) > maxBody {
		return nil, errors.New("response too large")
	}
	if err != nil {
		return nil, &responseReadError{cause: err}
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil || wire == nil {
		return nil, errors.New("invalid response")
	}
	if number(wire, "schema_version") != 1 {
		return nil, errors.New("unsupported response version")
	}
	return wire, nil
}

func number(wire map[string]json.RawMessage, key string) int64 {
	var value int64
	if len(wire[key]) == 0 || json.Unmarshal(wire[key], &value) != nil {
		return -1
	}
	return value
}

func stringField(wire map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(wire[key], &value) != nil {
		return ""
	}
	return value
}

func boolField(wire map[string]json.RawMessage, key string) (bool, bool) {
	var value bool
	if len(wire[key]) == 0 || json.Unmarshal(wire[key], &value) != nil {
		return false, false
	}
	return value, true
}

func validInstant(value string) bool {
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	return err == nil && parsed.Year() >= 1 && parsed.Year() <= 9999 &&
		parsed.UTC().Format("2006-01-02T15:04:05.000Z") == value
}

func validID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if char < '0' || (char > '9' && char < 'a') || char > 'f' {
			return false
		}
	}
	return value[14] == '4' && strings.ContainsRune("89ab", rune(value[19]))
}

type EnrollmentResult struct {
	HostID       string
	AgentID      string
	CredentialID string
	Generation   uint64
	Interval     int
	StaleAfter   int
}

func (client *Client) Enroll(ctx context.Context, pending Enrollment) (EnrollmentResult, error) {
	var result EnrollmentResult
	if !validAgentVersion(pending.AgentVersion) {
		return result, errors.New("invalid pending request")
	}
	credentialID := strings.SplitN(strings.TrimPrefix(pending.Credential, "tw_a_"), ".", 2)[0]
	wire, err := client.post(ctx, "/api/v1/agent/enroll", pending.Token, map[string]any{
		"schema_version": 1, "request_id": pending.RequestID,
		"credential": pending.Credential, "hostname": pending.Hostname,
		"os_id": pending.OSID, "os_version": pending.OSVersion,
		"architecture": pending.Architecture, "agent_version": pending.AgentVersion,
	}, true)
	if err != nil {
		return result, err
	}
	generation := number(wire, "generation")
	if generation < 1 || generation > 9007199254740991 {
		return result, errors.New("invalid enrollment response")
	}
	result = EnrollmentResult{HostID: stringField(wire, "host_id"),
		AgentID: stringField(wire, "agent_id"), CredentialID: stringField(wire, "credential_id"),
		Generation: uint64(generation),
		Interval:   int(number(wire, "heartbeat_interval_seconds")),
		StaleAfter: int(number(wire, "stale_after_seconds"))}
	if !validID(result.HostID) || !validID(result.AgentID) ||
		result.CredentialID != credentialID || result.Generation < 1 ||
		result.Interval < 10 || result.Interval > 300 || result.StaleAfter < 3*result.Interval ||
		result.StaleAfter > 3600 {
		return EnrollmentResult{}, errors.New("invalid enrollment response")
	}
	return result, nil
}

type HeartbeatResult struct {
	Sequence   uint64
	AcceptedAt string
	Duplicate  bool
	Interval   int
	StaleAfter int
}

func (client *Client) Heartbeat(ctx context.Context, credential string, pending PendingHeartbeat) (HeartbeatResult, error) {
	var result HeartbeatResult
	wire, err := client.post(ctx, "/api/v1/agent/heartbeat", credential, map[string]any{
		"schema_version": 1, "sequence": pending.Sequence,
		"sent_at": pending.SentAt, "agent_version": pending.AgentVersion,
	}, false)
	if err != nil {
		return result, err
	}
	duplicate, valid := boolField(wire, "duplicate")
	sequence := number(wire, "sequence")
	if sequence < 1 {
		return result, errors.New("invalid heartbeat response")
	}
	result = HeartbeatResult{Sequence: uint64(sequence),
		AcceptedAt: stringField(wire, "accepted_at"), Duplicate: duplicate,
		Interval:   int(number(wire, "heartbeat_interval_seconds")),
		StaleAfter: int(number(wire, "stale_after_seconds"))}
	if !valid || result.Sequence != pending.Sequence || !validInstant(result.AcceptedAt) ||
		result.Interval < 10 || result.Interval > 300 || result.StaleAfter < 3*result.Interval ||
		result.StaleAfter > 3600 {
		return HeartbeatResult{}, errors.New("invalid heartbeat response")
	}
	return result, nil
}

// Give command adapters a safe category without exposing URLs, secrets or raw HTTP errors.
func ErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var response *ResponseError
	if errors.As(err, &response) {
		if response.Status == 401 {
			return "unauthorized"
		}
		if response.Status == 409 {
			return "conflict"
		}
		return fmt.Sprintf("server_%d", response.Status)
	}
	var netError net.Error
	if errors.As(err, &netError) {
		return "network"
	}
	return "unavailable"
}
