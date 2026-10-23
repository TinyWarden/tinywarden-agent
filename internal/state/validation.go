package state

import (
	"regexp"
	"strings"
	"time"
)

var TokenPattern = regexp.MustCompile(`^tw_e_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$`)

func ValidInstant(value string) bool {
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	return err == nil && parsed.Year() >= 1 && parsed.Year() <= 9999 &&
		parsed.UTC().Format("2006-01-02T15:04:05.000Z") == value
}

func ValidID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if char < '0' || (char > '9' && char < 'a') || char > 'f' {
			return false
		}
	}
	return value[14] == '4' && strings.ContainsRune("89ab", rune(value[19]))
}
