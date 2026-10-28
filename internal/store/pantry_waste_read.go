package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const pantryWasteScopeSQL = `SELECT ms.data_generation,h.timezone,
 (transaction_timestamp() AT TIME ZONE h.timezone)::date::text,transaction_timestamp(),ms.state='enabled'
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.id=$2 AND m.user_id=$3
 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=$5
 JOIN core.sessions s ON s.id=$6 AND s.user_id=u.id AND s.auth_version=u.auth_version AND s.revoked_at IS NULL
 AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='pantry' WHERE h.id=$1`

func readPantryWasteScope(ctx context.Context, q pantryQuerier, id auth.Identity) (PantryWasteScope, error) {
	var scope PantryWasteScope
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) || !value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return scope, ErrForbidden
	}
	var enabled bool
	err := q.QueryRow(ctx, pantryWasteScopeSQL, id.HouseholdID, id.MembershipID, id.UserID, id.Role, id.AuthVersion, id.SessionID).
		Scan(&scope.Generation, &scope.Timezone, &scope.Today, &scope.Now, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope, ErrForbidden
	}
	if err == nil && (!enabled || scope.Generation <= 0) {
		return scope, ErrNotFound
	}
	return scope, err
}

func (s *Store) PantryWasteContext(ctx context.Context, id auth.Identity) (PantryWasteScope, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return readPantryWasteScope(ctx, s.Pool, id)
}

func (s *Store) PantryWasteLot(ctx context.Context, id auth.Identity, lotID, selected string) (Record, error) {
	if !value.ValidUUID(lotID) || selected != "" && !value.ValidUUID(selected) {
		return nil, ErrNotFound
	}
	lotID, selected = strings.ToLower(lotID), strings.ToLower(selected)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readPantryWasteScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	lot, err := readPantryLot(ctx, tx, id, lotID)
	if err != nil {
		return nil, err
	}
	// Only immutable waste coordinates are loaded; notes, proof and cached payloads stay private.
	rows, err := tx.Query(ctx, `SELECT m.id,m.quantity_delta::text,coalesce(m.waste_reason,'unknown'),m.created_at,
 EXISTS(SELECT 1 FROM pantry.stock_movements r WHERE r.household_id=m.household_id AND r.reverses_movement_id=m.id)
 FROM pantry.stock_movements m WHERE m.household_id=$1 AND m.lot_id=$2 AND m.reason='waste'
 ORDER BY (m.id=$3::uuid) DESC,m.created_at DESC,m.id DESC LIMIT 51`, id.HouseholdID, lotID, optionalWasteUUID(selected))
	if err != nil {
		return nil, err
	}
	history := []Record{}
	for rows.Next() {
		var resource, quantity, code string
		var at time.Time
		var reversed bool
		if err = rows.Scan(&resource, &quantity, &code, &at, &reversed); err != nil {
			rows.Close()
			return nil, err
		}
		amount, parseErr := value.Decimal(quantity)
		if parseErr != nil || amount.Sign() >= 0 || pantryWasteReasons[code] == "" {
			rows.Close()
			return nil, Invalid{"Gespeicherten Verlust erneut prüfen"}
		}
		history = append(history, Record{"id": resource, "quantity": amount.Neg(amount).FloatString(6), "reason": code, "created_at": at, "reversed": reversed})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if selected != "" && (len(history) == 0 || String(history[0]["id"]) != selected) {
		return nil, ErrNotFound
	}
	more := len(history) > 50
	if more {
		history = history[:50]
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return Record{"context": scope, "lot": lot, "records": history, "more": more, "reasons": PantryWasteReasons()}, nil
}

func (s *Store) PantryWasteReport(ctx context.Context, id auth.Identity, expected PantryWasteScope, filter PantryWasteFilter, point PantryWastePoint) (PantryWasteReport, error) {
	var out PantryWasteReport
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	scope, err := readPantryWasteScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if scope.Generation != expected.Generation || scope.Timezone != expected.Timezone || scope.Today != expected.Today {
		return out, ErrConflict
	}
	filter, err = NormalizePantryWasteFilter(filter, scope.Today)
	if err != nil {
		return out, err
	}
	firstPage := point.AsOf.IsZero()
	if firstPage {
		if point.ID != "" || point.Fingerprint != "" || !point.Created.IsZero() {
			return out, Invalid{"Auswertung erneut beginnen"}
		}
		point.AsOf = expected.Now
	}
	if point.AsOf.IsZero() || point.AsOf.After(scope.Now) || point.ID != "" && (!value.ValidUUID(point.ID) || point.Created.IsZero() || len(point.Fingerprint) != 64) {
		return out, Invalid{"Auswertungsseite erneut laden"}
	}
	for _, selection := range []struct{ id, query string }{
		{filter.ProductID, "SELECT EXISTS(SELECT 1 FROM pantry.products WHERE household_id=$1 AND id=$2)"},
		{filter.LocationID, "SELECT EXISTS(SELECT 1 FROM pantry.locations WHERE household_id=$1 AND id=$2)"},
	} {
		if selection.id != "" {
			var exists bool
			if err = tx.QueryRow(ctx, selection.query, id.HouseholdID, selection.id).Scan(&exists); err != nil {
				return out, err
			}
			if !exists {
				return out, ErrNotFound
			}
		}
	}
	var start, end time.Time
	if err = tx.QueryRow(ctx, "SELECT $1::date::timestamp AT TIME ZONE $3,($2::date+1)::timestamp AT TIME ZONE $3", filter.From, filter.To, scope.Timezone).Scan(&start, &end); err != nil {
		return out, err
	}
	if firstPage && !point.AsOf.Before(end) {
		point.AsOf = end.Add(-time.Microsecond)
	}
	if !point.AsOf.Before(end) {
		return out, Invalid{"Auswertungsstichtag erneut laden"}
	}
	ids, err := pantryWasteCandidates(ctx, tx, id, filter, start, end, point.AsOf)
	if err != nil {
		return out, err
	}
	events, fingerprint, err := pantryWasteEvents(ctx, tx, id, ids, scope.Timezone, point.AsOf, filter.Reason)
	if err != nil {
		return out, err
	}
	out, err = summarizePantryWaste(events, point, fingerprint)
	if err != nil {
		return out, err
	}
	out.Context, out.Filter, out.AsOf, out.PotentialRows = scope, filter, point.AsOf, len(ids)
	if err = ctx.Err(); err != nil {
		return PantryWasteReport{}, err
	}
	return out, tx.Commit(ctx)
}

func pantryWasteCandidates(ctx context.Context, tx pgx.Tx, id auth.Identity, filter PantryWasteFilter, start, end, asOf time.Time) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT m.id,1024::bigint+6*(octet_length(p.name)::bigint+octet_length(loc.name)::bigint)
 FROM pantry.stock_movements m JOIN pantry.lots l ON l.household_id=m.household_id AND l.id=m.lot_id
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 WHERE m.household_id=$1 AND m.reason IN('waste','reversal') AND m.created_at>=$2 AND m.created_at<$3 AND m.created_at<=$4
 AND ($5::uuid IS NULL OR l.product_id=$5) AND ($6::uuid IS NULL OR l.location_id=$6)
 ORDER BY m.created_at DESC,m.id DESC LIMIT 10001`, id.HouseholdID, start, end, asOf, optionalWasteUUID(filter.ProductID), optionalWasteUUID(filter.LocationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	var bytes int64
	for rows.Next() {
		var resource string
		var size int64
		if err = rows.Scan(&resource, &size); err != nil {
			return nil, err
		}
		bytes += size
		if len(ids) >= pantryWasteLimit || size < 0 || bytes > 16<<20 {
			return nil, Invalid{"Mehr als 10000 mögliche Verlust-/Gegenbuchungen oder das sichere Auswertungsbudget überschritten. Auswahl enger begrenzen; es wurden keine Teilsummen berechnet."}
		}
		ids = append(ids, resource)
	}
	return ids, rows.Err()
}

func optionalWasteUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func pantryWasteEvents(ctx context.Context, tx pgx.Tx, id auth.Identity, ids []string, zone string, asOf time.Time, reasonFilter string) ([]PantryWasteRow, string, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	events := []PantryWasteRow{}
	if len(ids) == 0 {
		return events, hex.EncodeToString(hash.Sum(nil)), nil
	}
	rows, err := tx.Query(ctx, `SELECT m.id,m.lot_id,p.id,p.name,p.version,loc.id,loc.name,loc.version,
 m.created_at,(m.created_at AT TIME ZONE $3)::date::text,m.reason,p.unit_code,m.quantity_delta::text,
 CASE WHEN octet_length(CASE WHEN m.reason='waste' THEN m.waste_reason ELSE original.waste_reason END)>32 THEN '__invalid__'
  ELSE coalesce(CASE WHEN m.reason='waste' THEN m.waste_reason ELSE original.waste_reason END,'unknown') END,
 coalesce(original.id::text,''),coalesce(original.reason,''),coalesce(original.lot_id::text,''),coalesce(original.quantity_delta::text,''),coalesce(reversed.id::text,'')
 FROM pantry.stock_movements m JOIN pantry.lots l ON l.household_id=m.household_id AND l.id=m.lot_id
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 LEFT JOIN pantry.stock_movements original ON original.household_id=m.household_id AND original.id=m.reverses_movement_id AND original.created_at<=$4
 LEFT JOIN pantry.stock_movements reversed ON reversed.household_id=m.household_id AND reversed.reverses_movement_id=m.id AND reversed.created_at<=$4
 WHERE m.household_id=$1 AND m.id=ANY($2::uuid[]) ORDER BY m.created_at DESC,m.id DESC`, id.HouseholdID, ids, zone, asOf)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var row PantryWasteRow
		var delta, originalKind, originalLot, originalDelta string
		if err = rows.Scan(&row.ID, &row.LotID, &row.ProductID, &row.ProductName, &row.ProductVersion, &row.LocationID, &row.Location, &row.LocationVersion,
			&row.CreatedAt, &row.Day, &row.Kind, &row.Unit, &delta, &row.Reason, &row.SourceID, &originalKind, &originalLot, &originalDelta, &row.ReversedBy); err != nil {
			return nil, "", err
		}
		count++
		if err = encoder.Encode([]any{row, delta, originalKind, originalLot, originalDelta}); err != nil {
			return nil, "", err
		}
		quantity, parseErr := value.Decimal(delta)
		if parseErr != nil {
			return nil, "", Invalid{"Gespeicherte Verlustmenge erneut prüfen"}
		}
		if row.Kind == "reversal" {
			if row.SourceID == "" || originalLot != row.LotID {
				return nil, "", Invalid{"Gespeicherte Gegenbuchung ist nicht eindeutig zugeordnet"}
			}
			if originalKind != "waste" {
				continue
			}
			original, parseErr := value.Decimal(originalDelta)
			if parseErr != nil || quantity.Sign() <= 0 || original.Sign() >= 0 || quantity.Cmp(original.Neg(original)) != 0 {
				return nil, "", Invalid{"Gespeicherte Verlust-Gegenbuchung stimmt nicht mit ihrem Original überein"}
			}
		} else if row.Kind != "waste" || quantity.Sign() >= 0 {
			return nil, "", Invalid{"Gespeicherte Verlustbuchung ist nicht eindeutig auswertbar"}
		}
		row.Quantity = quantity.Abs(quantity).FloatString(6)
		if reasonFilter == "" || reasonFilter == row.Reason {
			events = append(events, row)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if count != len(ids) {
		return nil, "", ErrConflict
	}
	return events, hex.EncodeToString(hash.Sum(nil)), nil
}
