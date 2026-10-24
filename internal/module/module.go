// Package module defines compiled module contracts and validated input metadata.
package module

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/value"
)

type Field struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Kind      string   `json:"kind"`
	Required  bool     `json:"required"`
	Options   []string `json:"options,omitempty"`
	Reference string   `json:"reference,omitempty"`
	Default   string   `json:"default,omitempty"`
	Positive  bool     `json:"positive,omitempty"`
}

type Entity struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Primary   string  `json:"primary"`
	Fields    []Field `json:"fields"`
	Immutable bool    `json:"immutable"`
}

type Descriptor struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Short          string   `json:"short"`
	Icon           string   `json:"icon"`
	Order          int      `json:"order"`
	DefaultEnabled bool     `json:"default_enabled"`
	Entities       []Entity `json:"entities"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)

func ValidateRegistry(modules []Descriptor) error {
	seen := map[string]bool{}
	for _, m := range modules {
		if !identifier.MatchString(m.ID) || m.Title == "" || m.Icon == "" || seen[m.ID] {
			return fmt.Errorf("invalid or duplicate module %q", m.ID)
		}
		seen[m.ID] = true
		entities := map[string]bool{}
		for _, e := range m.Entities {
			if !identifier.MatchString(e.ID) || entities[e.ID] || e.Title == "" {
				return fmt.Errorf("invalid entity in %s", m.ID)
			}
			entities[e.ID] = true
			fields := map[string]bool{}
			for _, f := range e.Fields {
				if !identifier.MatchString(f.Name) || fields[f.Name] {
					return fmt.Errorf("invalid field in %s.%s", m.ID, e.ID)
				}
				fields[f.Name] = true
				switch f.Kind {
				case "text", "longtext", "date", "time", "quantity", "integer", "money", "boolean", "select", "reference":
				default:
					return fmt.Errorf("invalid field kind %s", f.Kind)
				}
			}
			if !fields[e.Primary] {
				return fmt.Errorf("missing primary field for %s.%s", m.ID, e.ID)
			}
		}
	}
	return nil
}

func (m Descriptor) Entity(id string) (Entity, bool) {
	for _, e := range m.Entities {
		if e.ID == id {
			return e, true
		}
	}
	return Entity{}, false
}
func (f Field) SQLType() string {
	switch f.Kind {
	case "reference":
		return "uuid"
	case "money", "integer":
		return "bigint"
	case "quantity":
		return "numeric"
	case "date":
		return "date"
	case "time":
		return "timestamptz"
	case "boolean":
		return "boolean"
	default:
		return "text"
	}
}

// Normalize accepts only fields in the compiled descriptor. Money strings are EUR
// amounts; JSON integer money values are already expressed in cents.
func (e Entity) Normalize(input map[string]json.RawMessage, partial bool) (map[string]any, error) {
	allowed := map[string]bool{}
	out := map[string]any{}
	for _, f := range e.Fields {
		allowed[f.Name] = true
		raw, exists := input[f.Name]
		if !exists {
			if partial {
				continue
			}
			if f.Default != "" {
				raw = json.RawMessage(strconv.Quote(f.Default))
			} else if f.Kind == "boolean" {
				out[f.Name] = false
				continue
			} else if f.Required {
				return nil, fmt.Errorf("%s: erforderlich", f.Label)
			} else {
				continue
			}
		}
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("%s: ungültiger Wert", f.Label)
		}
		if decoded == nil {
			if f.Required {
				return nil, fmt.Errorf("%s: erforderlich", f.Label)
			}
			out[f.Name] = nil
			continue
		}
		if f.Kind == "boolean" {
			b, ok := decoded.(bool)
			if !ok {
				if s, valid := decoded.(string); valid && (s == "true" || s == "false") {
					b = s == "true"
				} else {
					return nil, fmt.Errorf("%s: ungültiger Schalter", f.Label)
				}
			}
			out[f.Name] = b
			continue
		}
		s := strings.TrimSpace(fmt.Sprint(decoded))
		if len(s) > 20000 || !utf8.ValidString(s) {
			return nil, fmt.Errorf("%s: zu lang", f.Label)
		}
		if s == "" && !f.Required {
			if f.Kind == "text" || f.Kind == "longtext" {
				out[f.Name] = ""
			} else {
				out[f.Name] = nil
			}
			continue
		}
		switch f.Kind {
		case "text", "longtext":
			if s == "" || f.Kind == "text" && utf8.RuneCountInString(s) > 300 {
				return nil, fmt.Errorf("%s: ungültiger Text", f.Label)
			}
			out[f.Name] = s
		case "select":
			if !contains(f.Options, s) {
				return nil, fmt.Errorf("%s: unbekannte Auswahl", f.Label)
			}
			out[f.Name] = s
		case "reference":
			if !value.ValidUUID(s) {
				return nil, fmt.Errorf("%s: ungültige Referenz", f.Label)
			}
			out[f.Name] = s
		case "date":
			if !value.Date(s) {
				return nil, fmt.Errorf("%s: ungültiges Datum", f.Label)
			}
			out[f.Name] = s
		case "time":
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return nil, fmt.Errorf("%s: Datum mit Zeitzone erforderlich", f.Label)
			}
			out[f.Name] = t.Format(time.RFC3339)
		case "quantity":
			r, err := value.Decimal(s)
			if err != nil || f.Positive && r.Sign() <= 0 {
				return nil, fmt.Errorf("%s: gültige Menge erforderlich", f.Label)
			}
			out[f.Name] = r.FloatString(6)
		case "integer":
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || n < 0 || n > 1000000000 {
				return nil, fmt.Errorf("%s: ungültige ganze Zahl", f.Label)
			}
			out[f.Name] = n
		case "money":
			var n int64
			var err error
			if _, ok := decoded.(json.Number); ok {
				n, err = strconv.ParseInt(s, 10, 64)
			} else {
				n, err = value.Minor(s)
			}
			if err != nil || n == 0 || n > 1000000000000 || n < -1000000000000 || f.Positive && n < 0 {
				return nil, fmt.Errorf("%s: gültiger Betrag erforderlich", f.Label)
			}
			out[f.Name] = n
		}
	}
	for key := range input {
		if !allowed[key] {
			return nil, errors.New("unbekanntes oder geschütztes Feld: " + key)
		}
	}
	return out, nil
}

func contains(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func Text(name, label string, required bool) Field {
	return Field{Name: name, Label: label, Kind: "text", Required: required}
}
func Long(name, label string) Field { return Field{Name: name, Label: label, Kind: "longtext"} }
func Ref(name, label, target string, required bool) Field {
	return Field{Name: name, Label: label, Kind: "reference", Reference: target, Required: required}
}
func Qty(name, label string, required, positive bool) Field {
	return Field{Name: name, Label: label, Kind: "quantity", Required: required, Positive: positive}
}
func Date(name, label string, required bool) Field {
	return Field{Name: name, Label: label, Kind: "date", Required: required}
}
func Time(name, label string, required bool) Field {
	return Field{Name: name, Label: label, Kind: "time", Required: required}
}
func Bool(name, label string) Field { return Field{Name: name, Label: label, Kind: "boolean"} }
func Money(name, label string, positive bool) Field {
	return Field{Name: name, Label: label, Kind: "money", Required: true, Positive: positive}
}
func Int(name, label string) Field {
	return Field{Name: name, Label: label, Kind: "integer", Default: "0"}
}
func Select(name, label string, options ...string) Field {
	return Field{Name: name, Label: label, Kind: "select", Options: options, Required: true, Default: options[0]}
}
