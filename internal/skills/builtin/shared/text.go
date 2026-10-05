package shared

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

func ValidText(data []byte) bool {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	for _, char := range string(data) {
		if (char < 32 && char != '\n' && char != '\t') || char == 127 {
			return false
		}
	}
	return true
}

func Lines(data []byte) ([]string, bool) {
	if len(data) == 0 || data[len(data)-1] != '\n' || !ValidText(data) {
		return nil, false
	}
	return strings.Split(string(data[:len(data)-1]), "\n"), true
}

func Member(value, allowed string) bool {
	for _, item := range strings.Split(allowed, "|") {
		if value == item {
			return true
		}
	}
	return false
}
