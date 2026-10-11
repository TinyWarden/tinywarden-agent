package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Reject duplicate keys before typed decoding, including in nested objects.
func Decode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("invalid_json")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := scanJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("invalid_json")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func scanJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("invalid_json")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("duplicate_json_key")
			}
			seen[s] = true
			if err := scanJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid_json")
		}
	case json.Delim('['):
		for d.More() {
			if err := scanJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid_json")
		}
	case json.Delim(']'), json.Delim('}'):
		return errors.New("invalid_json")
	}
	return nil
}
