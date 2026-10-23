package trim

import (
	"strings"
	"testing"
	"time"
)

func TestSystemdTimestampBoundaries(t *testing.T) {
	for _, value := range []string{"", "@0", "@0.000000"} {
		if at, ok := timestamp(value); !ok || at != nil {
			t.Fatal("unset timestamp", value, at, ok)
		}
	}
	for _, value := range []string{"@1", "@1.999999", "Thu 1970-01-01 00:00:01 UTC"} {
		if at, ok := timestamp(value); !ok || at == nil || *at != 1 {
			t.Fatal("one second", value, at, ok)
		}
	}
	if at, ok := timestamp("@253402300799"); !ok || at == nil || *at != MaxEpoch {
		t.Fatal(at, ok)
	}
	for _, value := range []string{"@253402300800", "@-1", "@01", "@1.1234567", "1", "never", "n/a",
		"Fri 1970-01-01 00:00:01 UTC", "Thu 1970-01-01 00:00:01 CET", "Thu 1970-02-30 00:00:00 UTC"} {
		if _, ok := timestamp(value); ok {
			t.Fatal("unsupported timestamp", value)
		}
	}
}

func TestPropertiesRejectAmbiguityAndExtraData(t *testing.T) {
	if _, ok := properties([]byte("B=two\nA=one\n"), []string{"A", "B"}); !ok {
		t.Fatal("order should not matter")
	}
	for _, value := range []string{"A=one\nA=two\n", "A=one\nC=two\n", "A=one\n", "A=one\nB=two",
		"A=one\nB=two\nC=three\n", "A=" + strings.Repeat("x", 511) + "\nB=two\n"} {
		if _, ok := properties([]byte(value), []string{"A", "B"}); ok {
			t.Fatal("ambiguous properties", value)
		}
	}
	for _, value := range []string{"-1", "256", "1.5", "true", "", "9999"} {
		if _, ok := boundedNumber(value, 255); ok {
			t.Fatal("unsupported integer", value)
		}
	}
}

func TestUnverifiableObservationWindowCannotUseServiceEvidence(t *testing.T) {
	start := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	boot := "11111111-1111-1111-1111-111111111111"
	for _, w := range []Window{
		{FinishedAt: start},
		{StartedAt: start, FinishedAt: start.Add(-time.Nanosecond)},
		{StartedAt: start, FinishedAt: start.Add(31*time.Second + time.Nanosecond)},
	} {
		w.BootBefore, w.BootAfter = boot, boot
		if evidence, problem := parseFstrim(nil, nil, w); evidence != nil || problem != "clock_uncertain" {
			t.Fatal("invalid clock window", evidence, problem)
		}
	}
	for _, pair := range [][2]string{{"", ""}, {"invalid", "invalid"}, {boot, "22222222-2222-2222-2222-222222222222"}} {
		w := Window{StartedAt: start, FinishedAt: start.Add(time.Second), BootBefore: pair[0], BootAfter: pair[1]}
		if evidence, problem := parseFstrim(nil, nil, w); evidence != nil || problem != "boot_uncertain" {
			t.Fatal("invalid boot window", evidence, problem)
		}
	}
}
