package baseline

import (
	"regexp"
	"strconv"
	"strings"
)

var aptSummary = regexp.MustCompile(`^(0|[1-9][0-9]{0,6}) upgraded, (0|[1-9][0-9]{0,6}) newly installed, (0|[1-9][0-9]{0,6}) to remove and (0|[1-9][0-9]{0,6}) not upgraded\.$`)

func parsePackages(data []byte, mode string) (*PackageEvidence, string) {
	rows, ok := lines(data)
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
			if err != nil || count > MaxCount {
				return nil, "output_unsupported"
			}
			counts[i] = count
			total += count
		}
		if total > MaxCount {
			return nil, "output_unsupported"
		}
	}
	if counts == nil {
		return nil, "output_unsupported"
	}
	return &PackageEvidence{mode, counts[0], counts[1], counts[2], counts[3], "unverified", "unverified"}, "none"
}
