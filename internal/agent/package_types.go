package agent

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"
)

const packageQueueLimit = 8 << 20

var errPackageState = errors.New("package state unavailable")
var contentDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var packageSubject = regexp.MustCompile(`^(disk-local|package-updates|reboot-required|fstrim-status|[a-z][a-z0-9-]{0,63}/[a-z][a-z0-9-]{0,63})$`)

type packageManualRequest struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
}
type packageAssignment struct {
	Manual         *packageManualRequest `json:"manual_request,omitempty"`
	InstallationID string                `json:"installation_id"`
	ID             string                `json:"assignment_id,omitempty"`
	Subject        string                `json:"subject_key"`
	Digest         string                `json:"content_sha256"`
	ArchiveDigest  string                `json:"archive_sha256,omitempty"`
	ArchiveBytes   int64                 `json:"archive_bytes,omitempty"`
	Official       bool                  `json:"official,omitempty"`
	Applicability  string                `json:"applicability"`
	Settings       json.RawMessage       `json:"settings,omitempty"`
	Grants         []json.RawMessage     `json:"grants,omitempty"`
	Interval       int                   `json:"interval_seconds,omitempty"`
}
type packageResponse struct {
	ManualSupported bool                `json:"manual_runs_supported,omitempty"`
	SchemaVersion   int                 `json:"schema_version"`
	Generation      string              `json:"generation"`
	IssuedAt        string              `json:"issued_at"`
	ValidUntil      string              `json:"valid_until"`
	RuntimeReady    bool                `json:"runtime_ready"`
	Assignments     []packageAssignment `json:"assignments"`
}
type packageRun struct {
	ManualID      string          `json:"manual_request_id,omitempty"`
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"run_id"`
	Sequence      uint64          `json:"run_sequence"`
	AssignmentID  string          `json:"assignment_id"`
	StartedAt     string          `json:"started_at"`
	FinishedAt    string          `json:"finished_at"`
	Outcome       string          `json:"outcome"`
	Observation   json.RawMessage `json:"observation"`
}
type packagePending struct {
	InstallationID string          `json:"installation_id"`
	Body           json.RawMessage `json:"body"`
	Digest         string          `json:"digest"`
}
type packageActive struct {
	Entry packageAssignment `json:"entry"`
	Run   packageRun        `json:"run"`
}
type packageLedger struct {
	Consumed    map[string]string `json:"consumed_manual_requests,omitempty"`
	Scope       baselineScope     `json:"scope"`
	Cache       *packageResponse  `json:"cache"`
	ValidatedAt string            `json:"validated_at"`
	Sequences   map[string]uint64 `json:"sequences"`
	Pending     []packagePending  `json:"pending"`
	Active      *packageActive    `json:"active"`
	Paused      bool              `json:"paused"`
}

func validPackageResponse(scope baselineScope, response packageResponse) bool {
	if response.SchemaVersion != 1 || response.Generation != strconv.FormatUint(scope.Generation, 10) || len(response.Assignments) > 100 ||
		response.Assignments == nil || !validInstant(response.IssuedAt) || !validInstant(response.ValidUntil) {
		return false
	}
	issued, _ := time.Parse(time.RFC3339Nano, response.IssuedAt)
	expires, _ := time.Parse(time.RFC3339Nano, response.ValidUntil)
	if !expires.After(issued) || expires.Sub(issued) > 5*time.Minute {
		return false
	}
	ids := map[string]bool{}
	for _, entry := range response.Assignments {
		if !validID(entry.InstallationID) || ids[entry.InstallationID] || !contentDigest.MatchString(entry.Digest) || !packageSubject.MatchString(entry.Subject) {
			return false
		}
		ids[entry.InstallationID] = true
		if entry.ArchiveDigest != "" && (!contentDigest.MatchString(entry.ArchiveDigest) || entry.ArchiveBytes < 1 || entry.ArchiveBytes > 10<<20) || entry.ArchiveDigest == "" && entry.ArchiveBytes != 0 {
			return false
		}
		if entry.Manual != nil && (!response.ManualSupported || !validID(entry.Manual.ID) || !validInstant(entry.Manual.ExpiresAt) || entry.Applicability != "ready") {
			return false
		}
		if entry.Applicability == "unavailable" {
			if entry.ID != "" || len(entry.Settings) != 0 {
				return false
			}
			continue
		}
		if entry.Applicability != "ready" || !response.RuntimeReady || !validID(entry.ID) || entry.Interval < 60 || entry.Interval > 86400 || len(entry.Settings) > 65536 || len(entry.Settings) == 0 || len(entry.Grants) > 32 {
			return false
		}
		var settings map[string]json.RawMessage
		if decodeBaseline(entry.Settings, &settings) != nil || settings == nil || len(settings) > 64 {
			return false
		}
	}
	return true
}
