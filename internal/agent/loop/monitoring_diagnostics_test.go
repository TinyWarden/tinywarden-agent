package loop

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
)

func TestMonitoringDiagnosticsContainOnlyBoundedCategories(t *testing.T) {
	err := &controlplane.ResponseError{Status: 503, Code: "secret-bearing error", RetryAfter: time.Minute}
	d := NewMonitoringDiagnostic("upload", 3, err, 5, time.Second, 10*time.Second)
	text := d.JSON()
	for _, expected := range []string{`"event":"monitoring_attempt_failed"`, `"lane":"upload"`, `"pending":3`, `"retry_seconds":10`, `"http_status":503`} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing safe diagnosis", text)
		}
	}
	d = NewMonitoringDiagnostic("credential-in-lane", 1000, errors.New("secret-bearing error"), -1, -time.Second, time.Hour)
	if text = d.JSON(); strings.Contains(text, "secret") || strings.Contains(text, "credential") || !strings.Contains(text, `"pending":8`) {
		t.Fatal("unsafe monitoring diagnostic", text)
	}
}
