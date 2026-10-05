package packages

import (
	"github.com/TinyWarden/tinywarden-agent/internal/skills/builtin/shared"
	"github.com/TinyWarden/tinywarden-agent/internal/skills/protocol"
	"regexp"
	"strconv"
	"strings"
)

var aptSummary = regexp.MustCompile(`^(0|[1-9][0-9]{0,6}) upgraded, (0|[1-9][0-9]{0,6}) newly installed, (0|[1-9][0-9]{0,6}) to remove and (0|[1-9][0-9]{0,6}) not upgraded\.$`)

func parsePackages(data []byte, mode string) (*protocol.PackageEvidence, string) {
	rows, ok := shared.Lines(data)
	if !ok {
		return nil, "output_unsupported"
	}
	var counts []int
	for _, line := range rows {
		// Never normalize a partial/broken plan just because its four-count line
		// exists. No stderr or repository/package identity is retained.
		if strings.HasPrefix(line, "E:") || strings.HasPrefix(line, "W:") || strings.Contains(line, "not fully installed or removed") {
			return nil, "output_unsupported"
		}
		match := aptSummary.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if counts != nil {
			return nil, "output_unsupported"
		}
		counts = make([]int, 4)
		total := 0
		for i, text := range match[1:] {
			count, err := strconv.Atoi(text)
			if err != nil || count > protocol.MaxCount {
				return nil, "output_unsupported"
			}
			counts[i] = count
			total += count
		}
		if total > protocol.MaxCount {
			return nil, "output_unsupported"
		}
	}
	if counts == nil {
		return nil, "output_unsupported"
	}
	return &protocol.PackageEvidence{Mode: mode, Upgraded: counts[0], Installed: counts[1], Removed: counts[2], HeldBack: counts[3], IndexFreshness: "unverified", StateConsistency: "unverified"}, "none"
}
