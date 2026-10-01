package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// encoding/json alone accepts duplicate keys and replaces invalid UTF-8. Walk
// all tokens first, with a small nesting limit, then decode the exact shape.
func decodeStrict(data []byte, target any) error {
	if len(data) == 0 || !utf8.Valid(data) {
		return errPolicy
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if walkJSON(d, 0) != nil {
		return errPolicy
	}
	if _, err := d.Token(); err != io.EOF {
		return errPolicy
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if exactShape(data, reflect.TypeOf(target).Elem()) != nil || d.Decode(target) != nil {
		return errPolicy
	}
	return nil
}

// Go's JSON decoder matches struct fields without regard to case. The wire
// contract requires the exact keys and every field, including nullable status.
func exactShape(data []byte, shape reflect.Type) error {
	if shape.Kind() == reflect.Pointer {
		return nil
	}
	switch shape.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || len(object) != shape.NumField() {
			return errPolicy
		}
		for i := 0; i < shape.NumField(); i++ {
			field := shape.Field(i)
			value, ok := object[field.Tag.Get("json")]
			if !ok || exactShape(value, field.Type) != nil {
				return errPolicy
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return errPolicy
		}
		for _, value := range values {
			if exactShape(value, shape.Elem()) != nil {
				return errPolicy
			}
		}
	}
	return nil
}

func walkJSON(d *json.Decoder, depth int) error {
	if depth > 12 {
		return errPolicy
	}
	token, err := d.Token()
	if err != nil {
		return errPolicy
	}
	if s, ok := token.(string); ok && strings.ContainsRune(s, 0) {
		return errPolicy
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok || keys[key] || strings.ContainsRune(key, 0) {
				return errPolicy
			}
			keys[key] = true
			if walkJSON(d, depth+1) != nil {
				return errPolicy
			}
		}
	case '[':
		for d.More() {
			if walkJSON(d, depth+1) != nil {
				return errPolicy
			}
		}
	default:
		return errPolicy
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) ||
		(delim == '[' && end != json.Delim(']')) {
		return errPolicy
	}
	return nil
}
