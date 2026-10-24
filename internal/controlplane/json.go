package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

var ErrInvalidState = errors.New("invalid baseline state")

// Validate before map/struct decoding can erase duplicates or repair encoding.
func StrictJSON(data []byte) error {
	return strictJSONNodes(data, 8192)
}

func strictJSONNodes(data []byte, maxNodes int) error {
	if len(data) == 0 || !utf8.Valid(data) {
		return ErrInvalidState
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	if baselineTokens(d, 0, &nodes, maxNodes) != nil {
		return ErrInvalidState
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalidState
	}
	return nil
}

func baselineTokens(d *json.Decoder, depth int, nodes *int, maxNodes int) error {
	*nodes++
	if depth > 16 || *nodes > maxNodes {
		return ErrInvalidState
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalidState
	}
	if text, ok := token.(string); ok && strings.ContainsRune(text, 0) {
		return ErrInvalidState
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
				return ErrInvalidState
			}
			keys[key] = true
			if baselineTokens(d, depth+1, nodes, maxNodes) != nil {
				return ErrInvalidState
			}
		}
	case '[':
		for d.More() {
			if baselineTokens(d, depth+1, nodes, maxNodes) != nil {
				return ErrInvalidState
			}
		}
	default:
		return ErrInvalidState
	}
	end, err := d.Token()
	if err != nil || open == '{' && end != json.Delim('}') || open == '[' && end != json.Delim(']') {
		return ErrInvalidState
	}
	return nil
}

func Decode(data []byte, target any) error {
	return decodeNodes(data, target, 8192)
}

// DecodeArtifact keeps exact JSON/shape validation for byte-bounded local artifacts
// whose many rows exceed a control message's node quota. Callers must bound bytes.
// It does not relax the ordinary wire decoder or its nesting/duplicate checks.
func DecodeArtifact(data []byte, target any, maxNodes int) error {
	if maxNodes < 1 || maxNodes > 200000 {
		return ErrInvalidState
	}
	return decodeNodes(data, target, maxNodes)
}

func decodeNodes(data []byte, target any, maxNodes int) error {
	if strictJSONNodes(data, maxNodes) != nil || Shape(data, reflect.TypeOf(target).Elem()) != nil {
		return ErrInvalidState
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return ErrInvalidState
	}
	return nil
}

// Exact names and required fields, including false/zero; nullable pointers remain
// nullable. Optional pointer content is validated when present, never ignored.
func Shape(data []byte, t reflect.Type) error {
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil
		}
		return Shape(data, t.Elem())
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalidState
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || object == nil {
			return ErrInvalidState
		}
		allowed := map[string]bool{}
		for i := range t.NumField() {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "" || tag[0] == "-" {
				return ErrInvalidState
			}
			allowed[tag[0]] = true
			value, ok := object[tag[0]]
			if !ok && len(tag) == 2 && tag[1] == "omitempty" {
				continue
			}
			if !ok || Shape(value, field.Type) != nil {
				return ErrInvalidState
			}
		}
		for key := range object {
			if !allowed[key] {
				return ErrInvalidState
			}
		}
	case reflect.Slice:
		var array []json.RawMessage
		if json.Unmarshal(data, &array) != nil {
			return ErrInvalidState
		}
		for _, item := range array {
			if Shape(item, t.Elem()) != nil {
				return ErrInvalidState
			}
		}
	}
	return nil
}
