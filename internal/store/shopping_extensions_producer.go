package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type shoppingBeforeKey struct{}
type shoppingBefore struct {
	entity, id string
	record     Record
}
type shoppingChange struct {
	Schema  int      `json:"schema"`
	Kind    string   `json:"kind"`
	ListIDs []string `json:"list_ids"`
}
type shoppingChangeEvent struct {
	Entity   string          `json:"entity"`
	ID       string          `json:"id"`
	Shopping *shoppingChange `json:"shopping,omitempty"`
}

func shoppingWithBefore(ctx context.Context, mid, entity string, before Record) context.Context {
	if mid != "shopping" || (entity != "items" && entity != "lists") {
		return ctx
	}
	return context.WithValue(ctx, shoppingBeforeKey{}, shoppingBefore{entity: entity, id: String(before["id"]), record: before})
}

func shoppingSameContent(entity string, before, after Record) bool {
	fields := []string{"name", "description", "sort_order", "archived_at", "is_template", "store_id"}
	if entity == "items" {
		fields = []string{"list_id", "name", "quantity", "unit_code", "group_name", "note", "sort_order", "availability", "assignee_id", "product_id", "replacement_product_id"}
	}
	for _, field := range fields {
		a, _ := json.Marshal(before[field])
		b, _ := json.Marshal(after[field])
		if string(a) != string(b) {
			return false
		}
	}
	if entity == "items" && (before["checked_at"] != nil) != (after["checked_at"] != nil) {
		return false
	}
	return true
}

func shoppingEventPayload(ctx context.Context, mid, entity string, r Record) ([]byte, int, error) {
	if mid != "shopping" {
		raw, err := json.Marshal(map[string]any{"entity": entity, "id": r["id"]})
		return raw, 1, err
	}
	event := shoppingChangeEvent{Entity: entity, ID: String(r["id"])}
	version := 1
	if mid == "shopping" && (entity == "items" || entity == "lists") {
		before, hasBefore := ctx.Value(shoppingBeforeKey{}).(shoppingBefore)
		hasBefore = hasBefore && before.entity == entity && before.id == event.ID
		if !hasBefore || !shoppingSameContent(entity, before.record, r) {
			kind, lid := "list_changed", event.ID
			if entity == "items" {
				kind, lid = "item_changed", String(r["list_id"])
			}
			ids := []string{strings.ToLower(lid)}
			if hasBefore && entity == "items" {
				old := strings.ToLower(String(before.record["list_id"]))
				if old != ids[0] {
					ids = append(ids, old)
				}
			}
			for _, id := range ids {
				if !shoppingCanonicalUUID(id) {
					return nil, 0, Invalid{"Einkaufsereignis hat keinen gültigen Listenbezug"}
				}
			}
			sort.Strings(ids)
			event.Shopping = &shoppingChange{Schema: 1, Kind: kind, ListIDs: ids}
			version = 2
		}
	}
	raw, err := json.Marshal(event)
	return raw, version, err
}

func emitShoppingDeletion(ctx context.Context, tx pgx.Tx, id auth.Identity, entity string, before Record, generation int64) error {
	if entity != "items" && entity != "lists" {
		return nil
	}
	kind, lid := "list_deleted", String(before["id"])
	if entity == "items" {
		kind, lid = "item_deleted", String(before["list_id"])
	}
	if !value.ValidUUID(lid) || !value.ValidUUID(String(before["id"])) {
		return ErrConflict
	}
	raw, err := json.Marshal(shoppingChangeEvent{Entity: entity, ID: String(before["id"]), Shopping: &shoppingChange{Schema: 1, Kind: kind, ListIDs: []string{strings.ToLower(lid)}}})
	if err != nil {
		return err
	}
	eid := value.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,event_version,actor_user_id,payload,dedupe_key,created_at)
 VALUES($1,$2,'shopping',$3,'record_changed',2,$4,$5,$1::uuid::text,clock_timestamp())`, eid, id.HouseholdID, generation, id.UserID, raw)
	return err
}

// Keep this producer in Core's existing transactional afterWrite contract.
// The module descriptor argument is intentional: caller never supplies SQL identifiers.
func shoppingProducerBefore(ctx context.Context, m module.Descriptor, e module.Entity, current Record) context.Context {
	return shoppingWithBefore(ctx, m.ID, e.ID, current)
}
