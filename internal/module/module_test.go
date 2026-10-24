package module_test

import (
	"encoding/json"
	"household/internal/module"
	"household/internal/modules"
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
