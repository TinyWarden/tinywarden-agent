package loop

import (
	"encoding/json"
	"time"
)

type Diagnostics struct {
	Heartbeat  func(HeartbeatDiagnostic)
	Monitoring func(MonitoringDiagnostic)
}

// Reuse the transport classifier and its sanitization; never log raw errors/data.
type MonitoringDiagnostic struct {
	Lane    string
	Pending int
	Detail  HeartbeatDiagnostic
}

func NewMonitoringDiagnostic(lane string, pending int, err error, attempt int, elapsed, retry time.Duration) MonitoringDiagnostic {
	return MonitoringDiagnostic{lane, pending, NewHeartbeatDiagnostic(err, attempt, elapsed, retry)}
}

func (d MonitoringDiagnostic) JSON() string {
	var fields map[string]any
	_ = json.Unmarshal([]byte(d.Detail.JSON()), &fields)
	if d.Detail.Event == "heartbeat_recovered" {
		fields["event"] = "monitoring_recovered"
	} else {
		fields["event"] = "monitoring_attempt_failed"
	}
	if d.Lane != "assignments" && d.Lane != "upload" && d.Lane != "work_wait" {
		d.Lane = "unknown"
	}
	fields["lane"] = d.Lane
	fields["pending"] = min(max(d.Pending, 0), 8)
	delete(fields, "stage")
	raw, _ := json.Marshal(fields)
	return string(raw)
}
