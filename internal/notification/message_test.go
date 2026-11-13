package notification

import (
	"strings"
	"testing"
)

func TestWireMessagesRemainVersionedAndFresh(t *testing.T) {
	const now = int64(1793707200)
	const id = "11111111-1111-4111-8111-111111111111"
	scope := strings.Repeat("a", 64)
	if !ValidMessage(1, "shopping_notice", id, scope, now+86400, now) {
		t.Fatal("legacy shopping wire contract stopped being readable")
	}
	for _, category := range Categories() {
		freshness := int64(category.Freshness.Seconds())
		if !ValidMessage(2, category.Category, id, scope, now+freshness, now) {
			t.Fatalf("compiled category rejected: %s", category.Category)
		}
		if ValidMessage(2, category.Category, id, scope, now+freshness+1, now) {
			t.Fatalf("excess freshness accepted: %s", category.Category)
		}
	}
	for _, tc := range []struct {
		name            string
		schema          int
		kind, id, scope string
		expires, now    int64
	}{
		{"expired", 2, "test", id, scope, now, now},
		{"unknown category", 2, "url", id, scope, now + 60, now},
		{"legacy category in new schema", 2, "shopping_notice", id, scope, now + 60, now},
		{"new category in legacy schema", 1, "cooking_timer", id, scope, now + 60, now},
		{"future schema", 3, "test", id, scope, now + 60, now},
		{"invalid ID", 2, "test", "../../settings", scope, now + 60, now},
		{"invalid scope", 2, "test", id, strings.Repeat("A", 64), now + 60, now},
		{"missing scope", 2, "test", id, "", now + 60, now},
		{"unsafe integer", 2, "test", id, scope, MaxRevision + 1, now},
		{"invalid clock", 2, "test", id, scope, 60, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ValidMessage(tc.schema, tc.kind, tc.id, tc.scope, tc.expires, tc.now) {
				t.Fatal("invalid wire message accepted")
			}
		})
	}
}
