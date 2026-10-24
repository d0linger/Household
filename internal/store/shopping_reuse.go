package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// copyShoppingList reads at most one bounded source snapshot. NOWAIT avoids
// waiting for an item editor that already owns its row and awaits the list lock.
func (s *Store) copyShoppingList(ctx context.Context, tx pgx.Tx, id auth.Identity, source, data Record, generation int64) ([]byte, error) {
	name := strings.TrimSpace(String(data["name"]))
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 120 || strings.ContainsRune(name, '\x00') {
		return nil, Invalid{"Name der neuen Liste mit 1 bis 120 Zeichen angeben"}
	}
	rows, err := tx.Query(ctx, "SELECT to_jsonb(t) FROM shopping.items t WHERE household_id=$1 AND list_id=$2 ORDER BY id LIMIT 501 FOR SHARE NOWAIT", id.HouseholdID, source["id"])
	if err != nil {
		return nil, shoppingContention(err)
	}
	items := make([]Record, 0, 50)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		item, decodeErr := decode(raw)
		if decodeErr != nil {
			rows.Close()
			return nil, decodeErr
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, shoppingContention(err)
	}
	if len(items) > 500 {
		return nil, Invalid{"Vorlagen mit höchstens 500 Artikeln kopieren; Liste zuerst aufteilen"}
	}
	m, _ := s.Module("shopping")
	lists, _ := m.Entity("lists")
	itemEntity, _ := m.Entity("items")
	result, err := save(ctx, tx, id, m, lists, value.UUID(), Record{"name": name, "description": source["description"], "store_id": source["store_id"], "sort_order": 0, "is_template": false}, true, 0)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		copyData := Record{"list_id": result["id"]}
		for _, field := range []string{"name", "product_id", "quantity", "unit_code", "group_name", "note", "sort_order"} {
			copyData[field] = item[field]
		}
		copied, copyErr := save(ctx, tx, id, m, itemEntity, value.UUID(), copyData, true, 0)
		if copyErr != nil {
			return nil, copyErr
		}
		if err = s.afterWrite(ctx, tx, id, m, itemEntity, copied, generation); err != nil {
			return nil, err
		}
	}
	result["url"] = "/m/shopping/lists/" + String(result["id"])
	return json.Marshal(result)
}

func shoppingContention(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "55P03" {
		return ErrConflict
	}
	return err
}
