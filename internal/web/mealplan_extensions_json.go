package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"household/internal/store"
)

// readPlanningExtensionJSON retains nullable prices while rejecting duplicate,
// case-ambiguous, unknown, oversized and structurally mismatched members.
func readPlanningExtensionJSON(w http.ResponseWriter, r *http.Request, into any, required ...string) error {
	invalid := store.Invalid{Message: "Planungsdaten sind unvollständig, ungültig oder zu groß"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return invalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 20000
	if err = planningJSONValue(decoder, reflect.TypeOf(into), 0, &remaining); err != nil {
		return invalid
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return invalid
	}
	for _, key := range append([]string{"generation", "plan_version", "week"}, required...) {
		if _, exists := members[key]; !exists {
			return invalid
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

func planningJSONFields(expected reflect.Type, fields map[string]reflect.Type) {
	for index := 0; index < expected.NumField(); index++ {
		field := expected.Field(index)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct {
			planningJSONFields(field.Type, fields)
			continue
		}
		if name != "" {
			fields[name] = field.Type
		}
	}
}

func planningJSONValue(decoder *json.Decoder, expected reflect.Type, depth int, remaining *int) error {
	*remaining -= 1
	if depth > 10 || *remaining < 0 || expected == nil {
		return errors.New("planning JSON bound exceeded")
	}
	nullable := expected.Kind() == reflect.Pointer
	for expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if nullable {
			return nil
		}
		return errors.New("unexpected null planning member")
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delimiter != '{' && delimiter != '[' || delimiter == '{' && expected.Kind() != reflect.Struct && expected.Kind() != reflect.Map ||
		delimiter == '[' && expected.Kind() != reflect.Slice {
		return errors.New("unexpected planning JSON structure")
	}
	fields := map[string]reflect.Type{}
	if expected.Kind() == reflect.Struct {
		planningJSONFields(expected, fields)
	}
	seen := map[string]bool{}
	for decoder.More() {
		var child reflect.Type
		if delimiter == '{' {
			key, keyErr := decoder.Token()
			name, valid := key.(string)
			if keyErr != nil || !valid || seen[name] {
				return errors.New("duplicate planning JSON member")
			}
			seen[name] = true
			if expected.Kind() == reflect.Map && expected.Key().Kind() == reflect.String {
				child = expected.Elem()
			} else {
				child = fields[name]
			}
			if child == nil {
				return errors.New("unknown planning JSON member")
			}
		} else {
			child = expected.Elem()
		}
		if err = planningJSONValue(decoder, child, depth+1, remaining); err != nil {
			return err
		}
	}
	if depth > 0 && expected.Kind() == reflect.Struct {
		for index := 0; index < expected.NumField(); index++ {
			field := expected.Field(index)
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.IsExported() && name != "" && name != "-" && !strings.Contains(","+options+",", ",omitempty,") && !seen[name] {
				return errors.New("missing required planning JSON member")
			}
		}
	}
	closing, err := decoder.Token()
	if err != nil || delimiter == '{' && closing != json.Delim('}') || delimiter == '[' && closing != json.Delim(']') {
		return errors.New("incomplete planning JSON structure")
	}
	return nil
}
