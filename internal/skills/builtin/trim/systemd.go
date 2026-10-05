package trim

import (
	"github.com/TinyWarden/tinywarden-agent/internal/skills/builtin/shared"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var epoch = regexp.MustCompile(`^@(0|[1-9][0-9]{0,11})(?:\.[0-9]{1,6})?$`)
var bootID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func properties(data []byte, keys []string) (map[string]string, bool) {
	rows, ok := shared.Lines(data)
	if !ok || len(rows) != len(keys) {
		return nil, false
	}
	values := make(map[string]string)
	for _, line := range rows {
		key, value, ok := strings.Cut(line, "=")
		if !ok || len(line) > 512 {
			return nil, false
		}
		if _, exists := values[key]; exists {
			return nil, false
		}
		values[key] = value
	}
	for _, key := range keys {
		if _, exists := values[key]; !exists {
			return nil, false
		}
	}
	return values, true
}

// systemctl mixes C-locale UTC dates and epoch presentation by property. Output
// carries seconds precision; fractional epoch input is deliberately floored.
func timestamp(value string) (*int64, bool) {
	if value == "" {
		return nil, true
	}
	var second int64
	if parts := epoch.FindStringSubmatch(value); parts != nil {
		second, _ = strconv.ParseInt(parts[1], 10, 64)
	} else {
		date, err := time.Parse("Mon 2006-01-02 15:04:05 UTC", value)
		if err != nil || date.Format("Mon 2006-01-02 15:04:05 UTC") != value {
			return nil, false
		}
		second = date.Unix()
	}
	if second < 0 || second > MaxEpoch {
		return nil, false
	}
	if second == 0 {
		return nil, true
	}
	return &second, true
}

func condition(values map[string]string) (Condition, bool) {
	if !shared.Member(values["ConditionResult"], "yes|no") {
		return Condition{}, false
	}
	at, ok := timestamp(values["ConditionTimestamp"])
	if !ok {
		return Condition{}, false
	}
	if at == nil {
		return Condition{}, true
	} // Default no is unevaluated, not skipped.
	passed := values["ConditionResult"] == "yes"
	return Condition{Passed: &passed, CheckedAt: at}, true
}

func states(values map[string]string) bool {
	return shared.Member(values["LoadState"], "loaded|not-found|error|bad-setting|masked|merged|stub") &&
		shared.Member(values["ActiveState"], "active|reloading|inactive|failed|activating|deactivating|refreshing|maintenance")
}

func boundedNumber(value string, max int) (int, bool) {
	if value == "" || len(value) > 3 {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n <= max
}
