package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"syscall"
	"time"
)

type HeartbeatOutcome struct{ Duplicate bool }

type heartbeatStageError struct {
	stage string
	cause error
}

func (e *heartbeatStageError) Error() string { return "heartbeat failed" }
func (e *heartbeatStageError) Unwrap() error { return e.cause }
func heartbeatError(stage string, err error) error {
	return &heartbeatStageError{stage: stage, cause: err}
}

// Diagnostics contain only categories chosen here and bounded numeric fields.
// Raw network/response/state errors must never enter the journal.
type HeartbeatDiagnostic struct {
	Event        string `json:"event"`
	Attempt      int    `json:"attempt"`
	Stage        string `json:"stage"`
	Reason       string `json:"reason"`
	Retryable    bool   `json:"retryable"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	RetrySeconds int64  `json:"retry_seconds"`
	HTTPStatus   int    `json:"http_status,omitempty"`
}

func heartbeatDiagnostic(err error, attempt int, elapsed, retry time.Duration) HeartbeatDiagnostic {
	d := HeartbeatDiagnostic{Event: "heartbeat_attempt_failed", Attempt: attempt, Stage: "exchange", Reason: "other_transport",
		Retryable: err != nil && !terminal(err), ElapsedMS: elapsed.Milliseconds(), RetrySeconds: int64((retry + time.Second - 1) / time.Second)}
	if err == nil {
		d.Event = "heartbeat_recovered"
		d.Stage = "complete"
		d.Reason = "none"
		return d
	}
	var staged *heartbeatStageError
	if errors.As(err, &staged) {
		d.Stage = staged.stage
	}
	if d.Stage == "pending_state" || d.Stage == "ack_state" {
		d.Reason = "local_state"
		return d
	}
	if d.Stage == "cadence" {
		d.Reason = "invalid_response"
		return d
	}
	var response *ResponseError
	var dns *net.DNSError
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var header tls.RecordHeaderError
	var interrupted *responseReadError
	var network net.Error
	switch {
	case errors.As(err, &response):
		d.Reason = "server_status"
		d.HTTPStatus = response.Status
	case errors.As(err, &authority) || errors.As(err, &hostname) || errors.As(err, &invalid) || errors.As(err, &verification):
		d.Reason = "tls_validation"
	case errors.As(err, &header):
		d.Reason = "tls_handshake"
	case errors.As(err, &dns):
		d.Reason = "dns"
	case errors.As(err, &interrupted) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		d.Reason = "response_interrupted"
	case errors.Is(err, syscall.ECONNREFUSED):
		d.Reason = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		d.Reason = "connection_reset"
	case errors.Is(err, context.DeadlineExceeded):
		d.Reason = "timeout"
	case errors.As(err, &network):
		if network.Timeout() {
			d.Reason = "timeout"
		}
	default:
		d.Reason = "invalid_response"
	}
	return d
}

// JSON revalidates every string: callers cannot insert error text into a log line.
func (d HeartbeatDiagnostic) JSON() string {
	valid := func(value string, allowed ...string) bool {
		for _, item := range allowed {
			if value == item {
				return true
			}
		}
		return false
	}
	if !valid(d.Event, "heartbeat_attempt_failed", "heartbeat_recovered") {
		d.Event = "heartbeat_attempt_failed"
	}
	if !valid(d.Stage, "pending_state", "exchange", "cadence", "ack_state", "complete") {
		d.Stage = "exchange"
	}
	if !valid(d.Reason, "none", "local_state", "invalid_response", "server_status", "tls_validation", "tls_handshake", "dns", "response_interrupted", "connection_refused", "connection_reset", "timeout", "other_transport") {
		d.Reason = "other_transport"
	}
	if d.Attempt < 0 || d.Attempt > 1000000 {
		d.Attempt = 0
	}
	if d.ElapsedMS < 0 || d.ElapsedMS > 86400000 {
		d.ElapsedMS = 0
	}
	if d.RetrySeconds < 0 || d.RetrySeconds > 900 {
		d.RetrySeconds = 0
	}
	if d.HTTPStatus < 100 || d.HTTPStatus > 599 {
		d.HTTPStatus = 0
	}
	data, _ := json.Marshal(d)
	return string(data)
}

func heartbeatRetryWait(err error, failures, interval int) time.Duration {
	if failures <= 4 {
		return retryWait(err, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}[failures-1])
	}
	base := time.Duration(min(interval, 30)) * time.Second
	delay := base + time.Duration(rand.Int64N(int64(base/10)+1))
	var response *ResponseError
	if errors.As(err, &response) && response.RetryAfter > delay {
		return response.RetryAfter
	}
	return delay
}
