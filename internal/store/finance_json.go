package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

const financeBodyLimit = 1 << 20

var financeRawType = reflect.TypeFor[json.RawMessage]()

// DecodeFinanceDocument applies the closed Finance JSON boundary to a typed
// ancillary request such as a bank review or receipt recognition command.
func DecodeFinanceDocument(raw []byte, into any) error { return decodeFinancePayload(raw, into) }

// DecodeFinanceCommand rejects ambiguous member names before JSON's permissive
// struct decoder can fold case or replace a duplicate value.
func DecodeFinanceCommand(raw []byte) (FinanceCommand, error) {
	var command FinanceCommand
	if err := decodeFinancePayload(raw, &command); err != nil {
		return command, err
	}
	return command, validateFinanceCommand(command)
}

func decodeFinancePayload(raw json.RawMessage, into any) error {
	invalid := Invalid{"Finanzangaben sind unvollständig, ungültig oder zu groß"}
	raw = bytes.TrimSpace(raw)
	target := reflect.TypeOf(into)
	if len(raw) == 0 || len(raw) > financeBodyLimit || !utf8.Valid(raw) || raw[0] != '{' ||
		target == nil || target.Kind() != reflect.Pointer || reflect.ValueOf(into).IsNil() {
		return invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	remaining := 30000
	if err := financeJSONValue(d, target, 0, &remaining); err != nil {
		return invalid
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if d.Decode(into) != nil || d.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

func financeJSONFields(t reflect.Type, fields map[string]reflect.Type, required map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct {
			financeJSONFields(field.Type, fields, required)
			continue
		}
		if name == "" || name == "-" {
			continue
		}
		fields[name] = field.Type
		required[name] = !strings.Contains(","+options+",", ",omitempty,")
	}
}

func financeJSONValue(d *json.Decoder, expected reflect.Type, depth int, remaining *int) error {
	*remaining -= 1
	if depth > 16 || *remaining < 0 {
		return errors.New("finance JSON bound exceeded")
	}
	nullable := expected != nil && expected.Kind() == reflect.Pointer
	for expected != nil && expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	if expected == financeRawType {
		expected = nil // Closed payload type is validated by its action decoder.
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if nullable || expected == nil {
			return nil
		}
		return errors.New("unexpected null finance member")
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil // The final typed decoder checks scalar types and bounds.
	}
	if delim != '{' && delim != '[' {
		return errors.New("unexpected finance JSON delimiter")
	}
	if expected != nil && ((delim == '{' && expected.Kind() != reflect.Struct && expected.Kind() != reflect.Map) ||
		(delim == '[' && expected.Kind() != reflect.Slice && expected.Kind() != reflect.Array)) {
		return errors.New("unexpected finance JSON shape")
	}
	fields, required := map[string]reflect.Type{}, map[string]bool{}
	if expected != nil && expected.Kind() == reflect.Struct {
		financeJSONFields(expected, fields, required)
	}
	seen := map[string]bool{}
	for d.More() {
		var child reflect.Type
		if delim == '{' {
			key, keyErr := d.Token()
			name, valid := key.(string)
			if keyErr != nil || !valid || seen[name] {
				return errors.New("duplicate finance JSON member")
			}
			seen[name] = true
			if expected != nil {
				if expected.Kind() == reflect.Map && expected.Key().Kind() == reflect.String {
					child = expected.Elem()
				} else {
					child = fields[name]
				}
				if child == nil {
					return errors.New("unknown finance JSON member")
				}
			}
		} else if expected != nil {
			child = expected.Elem()
		}
		if err = financeJSONValue(d, child, depth+1, remaining); err != nil {
			return err
		}
	}
	for name, mandatory := range required {
		if mandatory && !seen[name] {
			return errors.New("missing finance JSON member")
		}
	}
	closing, err := d.Token()
	if err != nil || delim == '{' && closing != json.Delim('}') || delim == '[' && closing != json.Delim(']') {
		return errors.New("incomplete finance JSON container")
	}
	return nil
}
