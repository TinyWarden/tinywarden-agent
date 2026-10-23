package release

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const Version = "0.0.18"
const Repository = "TinyWarden/tinywarden-agent"
const Capability = "agent_updates.v1"

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var UUIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func Compare(a, b string) (int, error) {
	if !versionPattern.MatchString(a) || !versionPattern.MatchString(b) {
		return 0, errors.New("invalid_version")
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range aa {
		x, _ := strconv.Atoi(aa[i])
		y, _ := strconv.Atoi(bb[i])
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	return 0, nil
}

func ValidVersion(s string) bool { return versionPattern.MatchString(s) }
func ValidDigest(s string) bool  { return digestPattern.MatchString(s) }
