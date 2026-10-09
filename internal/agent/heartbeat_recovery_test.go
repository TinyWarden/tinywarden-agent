package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestHeartbeatDegradedRetryIsBoundedAndHonorsRetryAfter(t *testing.T) {
	for _, interval := range []int{10, 60, 300} {
		base := time.Duration(min(interval, 30)) * time.Second
		for range 100 {
			delay := heartbeatRetryWait(&ResponseError{Status: 503}, 5, interval)
			if delay < base || delay > base+base/10 {
				t.Fatalf("heartbeat delay outside cadence: %v", delay)
			}
		}
	}
	for _, count := range []int{1, 5, 100} {
		delay := heartbeatRetryWait(&ResponseError{Status: 429, RetryAfter: 900 * time.Second}, count, 60)
		if delay != 900*time.Second {
			t.Fatalf("server delay not honored: %v", delay)
		}
	}
	if delay := degradedWait(&ResponseError{Status: 503}); delay < 300*time.Second {
		t.Fatal("other lanes lost their existing backoff")
	}
}

func TestDuplicateHeartbeatSchedulesOneFreshRequestWithOtherLanes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := []time.Time{}
		order := []string{}
		err := runHeartbeatLanes(ctx, 60, func(context.Context) (HeartbeatOutcome, error) {
			calls = append(calls, time.Now())
			order = append(order, "heartbeat")
			if len(calls) == 3 {
				cancel()
			}
			return HeartbeatOutcome{Duplicate: len(calls) == 1}, nil
		}, func(context.Context) error { order = append(order, "assignment"); return nil }, func(string) {}, nil)
		if !errors.Is(err, context.Canceled) || len(calls) != 3 {
			t.Fatal(err, calls)
		}
		if calls[1].Sub(calls[0]) != time.Second {
			t.Fatal("duplicate acknowledgment did not promptly schedule fresh heartbeat", calls)
		}
		delay := calls[2].Sub(calls[1])
		if delay < 60*time.Second || delay > 66*time.Second {
			t.Fatal("fresh heartbeat did not resume normal cadence", delay)
		}
		if len(order) < 3 || order[1] != "assignment" {
			t.Fatal("fresh recovery starved another lane", order)
		}
	})
}

func TestHeartbeatFailuresReportSafeDiagnosticsAndRecoverWithoutLongSleep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := []time.Time{}
		records := []HeartbeatDiagnostic{}
		assignments := 0
		err := runHeartbeatLanes(ctx, 60, func(context.Context) (HeartbeatOutcome, error) {
			calls = append(calls, time.Now())
			if len(calls) <= 5 {
				return HeartbeatOutcome{}, heartbeatError("exchange", &ResponseError{Status: 503})
			}
			cancel()
			return HeartbeatOutcome{}, nil
		}, func(context.Context) error { assignments++; return nil }, func(string) {}, func(d HeartbeatDiagnostic) { records = append(records, d) })
		if !errors.Is(err, context.Canceled) || len(calls) != 6 || len(records) != 6 {
			t.Fatal(err, calls, records)
		}
		delay := calls[5].Sub(calls[4])
		if delay < 30*time.Second || delay > 33*time.Second {
			t.Fatal("degraded retry still exceeds contact grace", delay)
		}
		if records[4].HTTPStatus != 503 || records[4].RetrySeconds < 30 || records[4].RetrySeconds > 33 || !records[4].Retryable {
			t.Fatal(records[4])
		}
		if records[5].Event != "heartbeat_recovered" || records[5].Attempt != 6 || assignments < 1 {
			t.Fatal(records[5], assignments)
		}
	})
}

func TestTerminalHeartbeatReportsRejectionAndStops(t *testing.T) {
	records := []HeartbeatDiagnostic{}
	err := runHeartbeatLanes(context.Background(), 60, func(context.Context) (HeartbeatOutcome, error) {
		return HeartbeatOutcome{}, &ResponseError{Status: 401, Code: "untrusted secret"}
	}, func(context.Context) error { t.Fatal("rejected authority continued polling"); return nil }, func(string) {}, func(d HeartbeatDiagnostic) { records = append(records, d) })
	if err == nil || len(records) != 1 || records[0].Retryable || records[0].HTTPStatus != 401 || strings.Contains(records[0].JSON(), "untrusted") {
		t.Fatal(err, records)
	}
}

func TestHeartbeatDiagnosticCategoriesNeverExposeRawErrors(t *testing.T) {
	marker := "secret-credential https://private.example/token"
	cases := []struct {
		err           error
		stage, reason string
	}{
		{&net.DNSError{Err: marker, Name: marker}, "exchange", "dns"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "exchange", "connection_refused"},
		{context.DeadlineExceeded, "exchange", "timeout"},
		{x509.HostnameError{Host: marker}, "exchange", "tls_validation"},
		{&responseReadError{cause: errors.New(marker)}, "exchange", "response_interrupted"},
		{errors.New(marker), "ack_state", "local_state"},
		{errors.New(marker), "pending_state", "local_state"},
		{errors.New(marker), "cadence", "invalid_response"},
	}
	for _, c := range cases {
		d := heartbeatDiagnostic(heartbeatError(c.stage, c.err), 1, time.Second, 30*time.Second)
		if d.Reason != c.reason || d.Stage != c.stage || strings.Contains(d.JSON(), marker) || len(d.JSON()) > 512 {
			t.Fatal(d)
		}
	}
	d := HeartbeatDiagnostic{Event: marker, Stage: marker, Reason: marker, Attempt: -1, ElapsedMS: -1, RetrySeconds: 10000, HTTPStatus: 123456}
	if strings.Contains(d.JSON(), marker) || len(d.JSON()) > 512 {
		t.Fatal("diagnostic serialization accepted arbitrary text", d.JSON())
	}
}
