package store

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"
)

type PantryPriorityRow struct {
	Lot       Record
	CreatedAt time.Time
}

func (s *Store) PantryPriority(ctx context.Context, id auth.Identity, query, product, afterID, afterDate string, afterUnknown bool) ([]PantryPriorityRow, error) {
	if len(query) > 300 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') || product != "" && !value.ValidUUID(product) || afterID != "" && !value.ValidUUID(afterID) {
		return nil, Invalid{"Gültige Produkt- und Chargenauswahl erforderlich"}
	}
	if afterID != "" && !afterUnknown {
		if _, err := time.Parse(time.DateOnly, afterDate); err != nil {
			return nil, Invalid{"Ungültiger Datumscursor; Auswahl erneut starten"}
		}
	}
	rows, err := s.Pool.Query(ctx, `WITH candidates AS (
 SELECT l.*,least(l.use_by_on,l.opened_on+l.after_open_days,l.best_before_on) priority FROM pantry.lots l WHERE l.household_id=$1
 ) SELECT `+pantryLotJSON+`,l.created_at FROM candidates l
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE b.quantity>0 AND p.archived_at IS NULL AND loc.archived_at IS NULL
 AND ($2='' OR strpos(lower(p.name),lower($2))>0 OR strpos(lower(l.notes),lower($2))>0)
 AND (nullif($3,'')::uuid IS NULL OR p.id=nullif($3,'')::uuid)
 AND (nullif($4,'')::uuid IS NULL OR
  ($5 AND l.priority IS NULL AND l.id>nullif($4,'')::uuid) OR
  (NOT $5 AND (l.priority>nullif($6,'')::date OR (l.priority=nullif($6,'')::date AND l.id>nullif($4,'')::uuid) OR l.priority IS NULL)))
 ORDER BY l.priority ASC NULLS LAST,l.id LIMIT 51`, id.HouseholdID, query, product, afterID, afterUnknown, afterDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PantryPriorityRow, 0, 51)
	for rows.Next() {
		var raw []byte
		var created time.Time
		if err = rows.Scan(&raw, &created); err != nil {
			return nil, err
		}
		lot, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err = pantryDates(lot); err != nil {
			return nil, err
		}
		result = append(result, PantryPriorityRow{Lot: lot, CreatedAt: created})
	}
	return result, rows.Err()
}
