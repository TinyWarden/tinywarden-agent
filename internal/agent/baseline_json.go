package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

var errBaselineState = errors.New("invalid baseline state")

// Validate before map/struct decoding can erase duplicates or repair encoding.
func baselineJSON(data []byte) error {
	if len(data) == 0 || !utf8.Valid(data) {
		return errBaselineState
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	if baselineTokens(d, 0, &nodes) != nil {
		return errBaselineState
	}
	if _, err := d.Token(); err != io.EOF {
		return errBaselineState
	}
	return nil
}

func baselineTokens(d *json.Decoder, depth int, nodes *int) error {
	*nodes++
	if depth > 16 || *nodes > 8192 {
		return errBaselineState
	}
	token, err := d.Token()
	if err != nil {
		return errBaselineState
	}
	if text, ok := token.(string); ok && strings.ContainsRune(text, 0) {
		return errBaselineState
	}
	open, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch open {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok || keys[key] || strings.ContainsRune(key, 0) {
				return errBaselineState
			}
			keys[key] = true
			if baselineTokens(d, depth+1, nodes) != nil {
				return errBaselineState
			}
		}
	case '[':
		for d.More() {
			if baselineTokens(d, depth+1, nodes) != nil {
				return errBaselineState
			}
		}
	default:
		return errBaselineState
	}
	end, err := d.Token()
	if err != nil || open == '{' && end != json.Delim('}') || open == '[' && end != json.Delim(']') {
		return errBaselineState
	}
	return nil
}

func decodeBaseline(data []byte, target any) error {
	if baselineJSON(data) != nil || baselineShape(data, reflect.TypeOf(target).Elem()) != nil {
		return errBaselineState
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return errBaselineState
	}
	return nil
}

// Exact names and required fields, including false/zero; nullable pointers remain
// nullable. Optional pointer content is validated when present, never ignored.
func baselineShape(data []byte, t reflect.Type) error {
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil
		}
		return baselineShape(data, t.Elem())
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errBaselineState
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || object == nil {
			return errBaselineState
		}
		allowed := map[string]bool{}
		for i := range t.NumField() {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "" || tag[0] == "-" {
				return errBaselineState
			}
			allowed[tag[0]] = true
			value, ok := object[tag[0]]
			if !ok && len(tag) == 2 && tag[1] == "omitempty" {
				continue
			}
			if !ok || baselineShape(value, field.Type) != nil {
				return errBaselineState
			}
		}
		for key := range object {
			if !allowed[key] {
				return errBaselineState
			}
		}
	case reflect.Slice:
		var array []json.RawMessage
		if json.Unmarshal(data, &array) != nil {
			return errBaselineState
		}
		for _, item := range array {
			if baselineShape(item, t.Elem()) != nil {
				return errBaselineState
			}
		}
	}
	return nil
}
