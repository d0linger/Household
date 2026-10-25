package store

import (
	"context"
	"errors"
	"testing"

	"household/internal/auth"
	"household/internal/value"
)

func TestIngredientComparisonMissingInformationIsExplicit(t *testing.T) {
	catalog := value.UUID()
	for _, tc := range []struct {
		name, cid, want string
		mapping         Record
		enabled         bool
	}{
		{"name alone never assigns", "", "catalog_unassigned", nil, true},
		{"historical deleted identity", catalog, "catalog_missing", nil, true},
		{"archived source", catalog, "catalog_archived", Record{"catalog_archived": true}, true},
		{"hidden target", catalog, "pantry_disabled", Record{"binding_id": "binding", "assigned": true}, false},
		{"no binding", catalog, "product_unassigned", Record{}, true},
		{"versioned tombstone", catalog, "product_unassigned", Record{"binding_id": "binding", "assigned": false}, true},
		{"purged target generation", catalog, "binding_stale", Record{"binding_id": "binding", "assigned": true, "target_current": false}, true},
		{"deleted endpoint", catalog, "product_missing", Record{"binding_id": "binding", "assigned": true, "target_current": true}, true},
		{"archived endpoint", catalog, "product_archived", Record{"binding_id": "binding", "assigned": true, "target_current": true, "product_id": "product", "product_archived": true}, true},
		{"explicit live endpoint", catalog, "", Record{"binding_id": "binding", "assigned": true, "target_current": true, "product_id": "product"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := comparisonMappingReason(tc.cid, tc.mapping, tc.enabled); got != tc.want {
				t.Fatalf("reason=%q want=%q", got, tc.want)
			}
		})
	}
	private := Record{"id": "binding", "version": int64(2), "product_id": "hidden-target", "notes": "private"}
	redacted := publicIngredientBinding(private, false)
	if redacted["product_id"] != nil || redacted["notes"] != nil || redacted["assigned"] != true || redacted["version"] != int64(2) {
		t.Fatal("disabled-target conflict response disclosed endpoint or lost review version", redacted)
	}
	if got := publicIngredientBinding(nil, false); got["assigned"] != false || got["product_id"] != nil || got["version"] != int64(0) {
		t.Fatal("absent binding did not retain explicit unassigned state", got)
	}
}

func TestIngredientComparisonRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	s := &Store{}
	for _, in := range []struct{ rid, version, portions string }{
		{value.UUID(), "", "0"}, {value.UUID(), "", "NaN"}, {"foreign-path", "", "1"}, {value.UUID(), "bad-version", "1"},
	} {
		_, err := s.RecipePantryComparison(context.Background(), auth.Identity{}, in.rid, in.version, in.portions)
		if err == nil {
			t.Fatal("invalid comparison touched database", in)
		}
		if in.rid == "foreign-path" && !errors.Is(err, ErrNotFound) {
			t.Fatal("malformed resource did not use opaque not-found response", err)
		}
	}
}
