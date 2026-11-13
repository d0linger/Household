package secretbox

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// ParseConfig rejects duplicate fields, noncanonical field names, unknown fields,
// nulls, trailing input and excessive input before constructing an immutable ring.
func ParseConfig(data []byte) (*Keyring, error) {
	if len(data) == 0 || len(data) > MaxConfigSize || !utf8.Valid(data) {
		return nil, ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !uniqueValue(decoder, 0) {
		return nil, ErrInvalidConfig
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalidConfig
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) < 3 || len(fields) > 4 {
		return nil, ErrInvalidConfig
	}
	for key, value := range fields {
		if key != "version" && key != "active" && key != "legacy" && key != "keys" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrInvalidConfig
		}
	}
	for _, key := range []string{"version", "active", "keys"} {
		if _, exists := fields[key]; !exists {
			return nil, ErrInvalidConfig
		}
	}
	var cfg ringConfig
	if json.Unmarshal(data, &cfg) != nil {
		return nil, ErrInvalidConfig
	}
	if _, exists := fields["legacy"]; exists && cfg.Legacy == "" {
		return nil, ErrInvalidConfig
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(fields["keys"], &entries) != nil || len(entries) > MaxKeys {
		return nil, ErrInvalidConfig
	}
	for _, entry := range entries {
		if len(entry) != 2 {
			return nil, ErrInvalidConfig
		}
		for _, key := range []string{"id", "secret"} {
			value, exists := entry[key]
			if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, ErrInvalidConfig
			}
		}
	}
	return build(cfg)
}

func uniqueValue(decoder *json.Decoder, depth int) bool {
	if depth > 4 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return true
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok || seen[key] || len(seen) >= 16 {
				return false
			}
			seen[key] = true
			if !uniqueValue(decoder, depth+1) {
				return false
			}
		}
		closing, err := decoder.Token()
		return err == nil && closing == json.Delim('}')
	case '[':
		count := 0
		for decoder.More() {
			count++
			if count > MaxKeys || !uniqueValue(decoder, depth+1) {
				return false
			}
		}
		closing, err := decoder.Token()
		return err == nil && closing == json.Delim(']')
	default:
		return false
	}
}
