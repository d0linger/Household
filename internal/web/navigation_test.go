package web

import (
	"reflect"
	"testing"

	"household/internal/module"
)

func TestValidModuleOrder(t *testing.T) {
	registry := []module.Descriptor{{ID: "shopping"}, {ID: "fuel"}, {ID: "recipes"}}
	for _, test := range []struct {
		name  string
		order []string
		valid bool
	}{
		{"default", nil, true},
		{"reset", []string{}, true},
		{"partial", []string{"recipes"}, true},
		{"all", []string{"recipes", "shopping", "fuel"}, true},
		{"duplicate", []string{"fuel", "fuel"}, false},
		{"unknown", []string{"external"}, false},
		{"empty_id", []string{""}, false},
		{"mixed_case", []string{"Fuel"}, false},
		{"too_many", []string{"fuel", "shopping", "recipes", "fuel"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validModuleOrder(test.order, registry); got != test.valid {
				t.Fatalf("validModuleOrder(%v)=%v, want %v", test.order, got, test.valid)
			}
		})
	}
}

func TestOrderedModules(t *testing.T) {
	registry := []module.Descriptor{{ID: "shopping"}, {ID: "fuel"}, {ID: "recipes"}, {ID: "pantry"}}
	for _, test := range []struct {
		name  string
		order []string
		want  []string
	}{
		{"default", nil, []string{"shopping", "fuel", "recipes", "pantry"}},
		{"remember_disabled", []string{"recipes", "fuel"}, []string{"recipes", "fuel", "shopping", "pantry"}},
		{"new_modules_append", []string{"fuel", "shopping", "recipes"}, []string{"fuel", "shopping", "recipes", "pantry"}},
		{"stale_ids_ignored", []string{"removed", "fuel", "fuel"}, []string{"fuel", "shopping", "recipes", "pantry"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := orderedModules(registry, test.order)
			ids := make([]string, len(got))
			for index, descriptor := range got {
				ids[index] = descriptor.ID
			}
			if !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("order %v, want %v", ids, test.want)
			}
			got[0].ID = "must_not_leak"
			if registry[0].ID != "shopping" || registry[1].ID != "fuel" {
				t.Fatal("personal ordering changed the shared registry")
			}
		})
	}
}
