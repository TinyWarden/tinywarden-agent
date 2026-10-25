package actioncontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Canonical hashes only protocol integers and strings. Domain numbers travel in
// exact JSON strings and are never reserialized into an authority digest.
func Canonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return nil, errors.New("action_canonical_invalid")
	}
	return canonical(decoded)
}

func quote(value string) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	var out strings.Builder
	for _, r := range strings.TrimSuffix(buffer.String(), "\n") {
		if r < 127 {
			out.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			a, b := utf16.EncodeRune(r)
			fmt.Fprintf(&out, "\\u%04x\\u%04x", a, b)
		}
	}
	return []byte(out.String())
}

func canonical(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return []byte("null"), nil
	case bool:
		if v {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case string:
		return quote(v), nil
	case json.Number:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || n < -(1<<53-1) || n > 1<<53-1 {
			return nil, errors.New("action_canonical_invalid")
		}
		return []byte(strconv.FormatInt(n, 10)), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			raw, err := canonical(item)
			if err != nil {
				return nil, err
			}
			parts = append(parts, string(raw))
		}
		return []byte("[" + strings.Join(parts, ",") + "]"), nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			raw, err := canonical(v[key])
			if err != nil {
				return nil, err
			}
			parts = append(parts, string(quote(key))+":"+string(raw))
		}
		return []byte("{" + strings.Join(parts, ",") + "}"), nil
	}
	return nil, errors.New("action_canonical_invalid")
}
