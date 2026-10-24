package module_test

import (
	"encoding/json"
	"household/internal/module"
	"household/internal/modules"
	"reflect"
	"strings"
	"testing"
)

func TestValidateRegistry(t *testing.T) {
	t.Parallel()
	registry := modules.Registry()
	if err := module.ValidateRegistry(registry); err != nil {
		t.Fatal(err)
	}
	registry = append(registry, registry[0])
	if err := module.ValidateRegistry(registry); err == nil {
		t.Fatal("duplicate module accepted")
	}
}

func TestValidateRegistryRejectsUnsafeMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*module.Descriptor)
	}{
		{name: "unsafe module identifier", change: func(d *module.Descriptor) { d.ID = `shopping;drop schema core` }},
		{name: "unsafe entity identifier", change: func(d *module.Descriptor) { d.Entities[0].ID = `entries"` }},
		{name: "unsafe field identifier", change: func(d *module.Descriptor) { d.Entities[0].Fields[0].Name = "title;" }},
		{name: "duplicate entity", change: func(d *module.Descriptor) { d.Entities = append(d.Entities, d.Entities[0]) }},
		{name: "duplicate field", change: func(d *module.Descriptor) {
			d.Entities[0].Fields = append(d.Entities[0].Fields, d.Entities[0].Fields[0])
		}},
		{name: "missing primary field", change: func(d *module.Descriptor) { d.Entities[0].Primary = "missing" }},
		{name: "unsupported field kind", change: func(d *module.Descriptor) { d.Entities[0].Fields[0].Kind = "raw_sql" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			descriptor := module.Descriptor{
				ID: "shopping", Title: "Einkaufen", Icon: "cart",
				Entities: []module.Entity{{
					ID: "entries", Title: "Einträge", Primary: "title",
					Fields: []module.Field{module.Text("title", "Name", true)},
				}},
			}
			tc.change(&descriptor)
			if err := module.ValidateRegistry([]module.Descriptor{descriptor}); err == nil {
				t.Fatal("unsafe compiled metadata accepted")
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	entity := module.Entity{ID: "entries", Title: "Entry", Primary: "title", Fields: []module.Field{module.Text("title", "Title", true), module.Money("amount", "Amount", false), module.Bool("checked", "Checked"), module.Qty("quantity", "Quantity", false, true)}}
	cases := []struct {
		name, input    string
		partial, valid bool
	}{{"valid", "{\"title\":\"Milk\",\"amount\":\"12,50\",\"quantity\":\"1.25\"}", false, true}, {"integer cents", "{\"title\":\"Milk\",\"amount\":1250}", false, true}, {"unknown field", "{\"title\":\"Milk\",\"household_id\":\"x\"}", false, false}, {"missing title", "{}", false, false}, {"partial", "{\"checked\":true}", true, true}, {"negative quantity", "{\"title\":\"Milk\",\"quantity\":\"-1\"}", false, false}, {"null required", "{\"title\":null}", false, false}, {"nonboolean", "{\"checked\":1}", true, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var data map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.input), &data); err != nil {
				t.Fatal(err)
			}
			_, err := entity.Normalize(data, tc.partial)
			if (err == nil) != tc.valid {
				t.Fatalf("valid %v, err %v", tc.valid, err)
			}
		})
	}
}

func TestNormalizeScalarContracts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field module.Field
		raw   string
		want  any
		valid bool
	}{
		{name: "trim text", field: module.Text("value", "Name", true), raw: `"  Milch  "`, want: "Milch", valid: true},
		{name: "unicode text limit", field: module.Text("value", "Name", true), raw: `"` + strings.Repeat("ä", 300) + `"`, want: strings.Repeat("ä", 300), valid: true},
		{name: "over text limit", field: module.Text("value", "Name", true), raw: `"` + strings.Repeat("ä", 301) + `"`},
		{name: "object is not text", field: module.Text("value", "Name", true), raw: `{"name":"Milch"}`},
		{name: "array is not text", field: module.Text("value", "Name", true), raw: `["Milch"]`},
		{name: "boolean is not text", field: module.Text("value", "Name", true), raw: `true`},
		{name: "number is not text", field: module.Text("value", "Name", true), raw: `1250`},
		{name: "NUL is not database text", field: module.Long("value", "Notiz"), raw: `"Milch\u0000Brot"`},
		{name: "invalid UTF8", field: module.Text("value", "Name", true), raw: "\"\xff\""},
		{name: "trailing JSON", field: module.Text("value", "Name", true), raw: `"Milch" "Brot"`},
		{name: "long text line breaks", field: module.Long("value", "Notiz"), raw: `"Milch\nBrot"`, want: "Milch\nBrot", valid: true},
		{name: "empty optional text", field: module.Long("value", "Notiz"), raw: `" "`, want: "", valid: true},
		{name: "null optional text", field: module.Long("value", "Notiz"), raw: `null`, valid: true},
		{name: "boolean input", field: module.Bool("value", "Erledigt"), raw: `false`, want: false, valid: true},
		{name: "import boolean", field: module.Bool("value", "Erledigt"), raw: `"true"`, want: true, valid: true},
		{name: "invalid boolean string", field: module.Bool("value", "Erledigt"), raw: `"yes"`},
		{name: "quantity exact", field: module.Qty("value", "Menge", true, true), raw: `0.123456`, want: "0.123456", valid: true},
		{name: "quantity comma", field: module.Qty("value", "Menge", true, true), raw: `"1,5"`, want: "1.500000", valid: true},
		{name: "quantity maximum", field: module.Qty("value", "Menge", true, true), raw: `999999999999.999999`, want: "999999999999.999999", valid: true},
		{name: "quantity zero forbidden", field: module.Qty("value", "Menge", true, true), raw: `"0"`},
		{name: "quantity precision", field: module.Qty("value", "Menge", true, true), raw: `"0.1234567"`},
		{name: "integer zero", field: module.Int("value", "Dauer"), raw: `0`, want: int64(0), valid: true},
		{name: "integer maximum", field: module.Int("value", "Dauer"), raw: `"1000000000"`, want: int64(1000000000), valid: true},
		{name: "integer overflow", field: module.Int("value", "Dauer"), raw: `9223372036854775808`},
		{name: "integer negative", field: module.Int("value", "Dauer"), raw: `-1`},
		{name: "money JSON cents", field: module.Money("value", "Betrag", false), raw: `1250`, want: int64(1250), valid: true},
		{name: "money string euros", field: module.Money("value", "Betrag", false), raw: `"1250"`, want: int64(125000), valid: true},
		{name: "money maximum", field: module.Money("value", "Betrag", false), raw: `1000000000000`, want: int64(1000000000000), valid: true},
		{name: "money above maximum", field: module.Money("value", "Betrag", false), raw: `1000000000001`},
		{name: "money zero forbidden", field: module.Money("value", "Betrag", false), raw: `"0.00"`},
		{name: "negative positive money", field: module.Money("value", "Betrag", true), raw: `-1`},
		{name: "fractional JSON cents", field: module.Money("value", "Betrag", false), raw: `12.50`},
		{name: "money exponent", field: module.Money("value", "Betrag", false), raw: `1e2`},
		{name: "valid date", field: module.Date("value", "Datum", true), raw: `"2024-02-29"`, want: "2024-02-29", valid: true},
		{name: "invalid leap day", field: module.Date("value", "Datum", true), raw: `"2100-02-29"`},
		{name: "time zone required", field: module.Time("value", "Zeit", true), raw: `"2026-10-24T12:30:00"`},
		{name: "time offset", field: module.Time("value", "Zeit", true), raw: `"2026-10-24T12:30:00+02:00"`, want: "2026-10-24T12:30:00+02:00", valid: true},
		{name: "UUID reference", field: module.Ref("value", "Liste", "lists", true), raw: `"00000000-0000-4000-8000-000000000001"`, want: "00000000-0000-4000-8000-000000000001", valid: true},
		{name: "malformed reference", field: module.Ref("value", "Liste", "lists", true), raw: `"00000000-0000-4000-8000-00000000000z"`},
		{name: "empty optional reference", field: module.Ref("value", "Liste", "lists", false), raw: `""`, valid: true},
		{name: "select allowlist", field: module.Select("value", "Einheit", "g", "kg"), raw: `"kg"`, want: "kg", valid: true},
		{name: "select unknown", field: module.Select("value", "Einheit", "g", "kg"), raw: `"l"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entity := module.Entity{Fields: []module.Field{tc.field}}
			got, err := entity.Normalize(map[string]json.RawMessage{"value": json.RawMessage(tc.raw)}, true)
			if (err == nil) != tc.valid {
				t.Fatalf("Normalize() = %v, %v; valid %v", got, err, tc.valid)
			}
			if tc.valid && !reflect.DeepEqual(got, map[string]any{"value": tc.want}) {
				t.Fatalf("Normalize() = %#v; want value %#v", got, tc.want)
			}
		})
	}
}

func TestNormalizeDefaultsAndPartialUpdates(t *testing.T) {
	t.Parallel()
	entity := module.Entity{Fields: []module.Field{
		module.Text("title", "Name", true), module.Bool("checked", "Erledigt"),
		module.Int("minutes", "Dauer"), module.Select("unit", "Einheit", "piece", "kg"),
	}}
	for _, tc := range []struct {
		name    string
		partial bool
		want    map[string]any
	}{
		{name: "create applies defaults", want: map[string]any{"title": "Milch", "checked": false, "minutes": int64(0), "unit": "piece"}},
		{name: "update leaves absent fields unchanged", partial: true, want: map[string]any{"title": "Milch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := entity.Normalize(map[string]json.RawMessage{"title": json.RawMessage(`"Milch"`)}, tc.partial)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Normalize() = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}
