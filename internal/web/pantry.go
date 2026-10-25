package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const pantryPageSize = 50

type pantryFilter struct {
	query, day, zone, product string
	cutoff                    any
	cursor                    referenceCursor
	scope                     string
}

// pantryDayEnd uses calendar arithmetic: a local day can contain 23 or 25 hours.
func pantryDayEnd(day, zone string, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	at, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil || at.Format("2006-01-02") != day || day > now.In(loc).Format("2006-01-02") || at.Year() < 1900 {
		return time.Time{}, store.Invalid{Message: "Gültigen Stichtag zwischen 1900 und heute auswählen"}
	}
	return at.AddDate(0, 0, 1), nil
}

func (s *Server) pantryFilter(r *http.Request) (pantryFilter, error) {
	f := pantryFilter{query: strings.TrimSpace(r.URL.Query().Get("q")), day: r.URL.Query().Get("at"), product: r.PathValue("id")}
	st, enabled := s.Store.State("pantry")
	if !enabled || f.product != "" && !value.ValidUUID(f.product) {
		return f, store.ErrNotFound
	}
	if len(f.query) > 300 || !utf8.ValidString(f.query) || strings.ContainsRune(f.query, '\x00') {
		return f, store.Invalid{Message: "Suchbegriff ist ungültig oder zu lang"}
	}
	if err := s.Pool.QueryRow(r.Context(), "SELECT timezone FROM core.households WHERE id=$1", identity(r).HouseholdID).Scan(&f.zone); err != nil {
		return f, err
	}
	if f.day != "" {
		cutoff, err := pantryDayEnd(f.day, f.zone, time.Now())
		if err != nil {
			return f, err
		}
		f.cutoff = cutoff
	}
	scope, err := json.Marshal([]any{"pantry-stock-v1", identity(r).HouseholdID, identity(r).UserID, st.Generation, f.query, f.day, f.zone, f.product})
	if err != nil {
		return f, err
	}
	f.scope = string(scope)
	f.cursor, err = s.readReferenceCursor(r.URL.Query().Get("cursor"), f.scope)
	return f, err
}

func (s *Server) pantryPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Vorratsbestand")
	p.Module, _ = s.Store.Module("pantry")
	p.Entity, _ = p.Module.Entity("products")
	p.View = "pantry"
	s.render(w, r, p)
}

// Both the page and full-selection totals read one repeatable-read snapshot.
// Pages are bounded; the totals and historical sums are never truncated to it.
func (s *Server) pantryStock(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	f, err := s.pantryFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var created, lastID any
	if f.cursor.ID != "" {
		created, lastID = f.cursor.Created, f.cursor.ID
	}
	base := pantryStockCTE(f.day != "")
	var total int64
	if err = tx.QueryRow(ctx, base+" SELECT count(*) FROM selected", identity(r).HouseholdID, f.cutoff, f.query).Scan(&total); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := tx.Query(ctx, base+` SELECT jsonb_build_object('id',p.id,'name',p.name,'unit',p.unit_code,
		'archived',p.archived_at IS NOT NULL,'minimum',p.low_stock_threshold::text,'target',p.target_stock::text,'stock',coalesce(s.stock,0)::text,
		'lot_count',s.lot_count,'location_count',s.location_count,'locations',coalesce(n.names,'[]'::jsonb),
		'next_best_before',s.best_before,'next_use_by',s.use_by,'later_corrections',s.corrections,'latest_correction_at',s.latest_correction),p.created_at
		FROM selected p LEFT JOIN LATERAL (
		 SELECT sum(quantity) stock,count(*) lot_count,count(DISTINCT location_id) FILTER(WHERE quantity>0) location_count,
		 min(best_before_on) FILTER(WHERE quantity>0) best_before,min(use_by_on) FILTER(WHERE quantity>0) use_by,
		 coalesce(sum(later_corrections),0) corrections,max(latest_correction_at) latest_correction FROM lot_state l WHERE l.product_id=p.id
		) s ON true LEFT JOIN LATERAL (
		 SELECT jsonb_agg(name ORDER BY name) names FROM (SELECT DISTINCT loc.name FROM lot_state l
		 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
		 WHERE l.product_id=p.id AND l.quantity>0 ORDER BY loc.name LIMIT 3) limited
		) n ON true WHERE ($4::timestamptz IS NULL OR (p.created_at,p.id)<($4,$5::uuid))
		ORDER BY p.created_at DESC,p.id DESC LIMIT 51`, identity(r).HouseholdID, f.cutoff, f.query, created, lastID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	records, next, err := s.pantryRecords(rows, f.scope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err = tx.Query(ctx, base+` SELECT p.unit_code,coalesce(sum(l.quantity),0)::text
		FROM selected p LEFT JOIN lot_state l ON l.product_id=p.id GROUP BY p.unit_code ORDER BY p.unit_code`, identity(r).HouseholdID, f.cutoff, f.query)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	totals := []store.Record{}
	for rows.Next() {
		var unit, quantity string
		if err = rows.Scan(&unit, &quantity); err != nil {
			break
		}
		totals = append(totals, store.Record{"unit": unit, "quantity": quantity})
	}
	rows.Close()
	if err == nil {
		err = rows.Err()
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, store.Record{"products": records, "total": total, "totals": totals, "next_cursor": next, "at": f.day, "timezone": f.zone, "cutoff_exclusive": f.cutoff})
}

func pantryStockCTE(historical bool) string {
	stock := `SELECT l.*,coalesce(b.quantity,0) quantity,0::bigint later_corrections,NULL::timestamptz latest_correction_at,b.last_recorded_at
		FROM pantry.lots l LEFT JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
		WHERE l.household_id=$1 AND ($2::timestamptz IS NULL OR l.created_at<$2)`
	if historical {
		stock = `SELECT l.*,coalesce(b.quantity,0) quantity,coalesce(b.later_corrections,0) later_corrections,b.latest_correction_at,b.last_recorded_at
		FROM pantry.lots l LEFT JOIN (
		 SELECT lot_id,sum(quantity_delta) FILTER(WHERE created_at<$2) quantity,
		 count(*) FILTER(WHERE created_at>=$2 AND reason IN('adjust','reversal')) later_corrections,
		 max(created_at) FILTER(WHERE created_at>=$2 AND reason IN('adjust','reversal')) latest_correction_at,
		 max(created_at) FILTER(WHERE created_at<$2) last_recorded_at
		 FROM pantry.stock_movements WHERE household_id=$1 GROUP BY lot_id
		) b ON b.lot_id=l.id WHERE l.household_id=$1 AND l.created_at<$2`
	}
	return "WITH lot_state AS MATERIALIZED (" + stock + `),selected AS (
		SELECT * FROM pantry.products WHERE household_id=$1 AND ($2::timestamptz IS NULL OR created_at<$2)
		AND ($3='' OR strpos(lower(name),lower($3))>0))`
}

func (s *Server) pantryLots(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	f, err := s.pantryFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, _ := s.Store.Module("pantry")
	e, _ := m.Entity("products")
	if _, err = s.Store.Get(r.Context(), identity(r), m, e, f.product); err != nil {
		s.fail(w, r, err)
		return
	}
	var created, lastID any
	if f.cursor.ID != "" {
		created, lastID = f.cursor.Created, f.cursor.ID
	}
	rows, err := s.Pool.Query(r.Context(), pantryStockCTE(f.day != "")+` SELECT jsonb_build_object(
		'id',l.id,'name',l.notes,'stock',l.quantity::text,'location',loc.name,'received_on',l.received_on,
		'best_before_on',l.best_before_on,'use_by_on',l.use_by_on,'opened_on',l.opened_on,
		'later_corrections',l.later_corrections,'latest_correction_at',l.latest_correction_at,'last_recorded_at',l.last_recorded_at),l.created_at
		FROM lot_state l JOIN selected p ON p.id=l.product_id
		JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
		WHERE l.product_id=$4 AND ($5::timestamptz IS NULL OR (l.created_at,l.id)<($5,$6::uuid))
		ORDER BY l.created_at DESC,l.id DESC LIMIT 51`, identity(r).HouseholdID, f.cutoff, f.query, f.product, created, lastID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	records, next, err := s.pantryRecords(rows, f.scope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, store.Record{"records": records, "next_cursor": next, "at": f.day, "timezone": f.zone})
}

func (s *Server) pantryRecords(rows pgx.Rows, scope string) ([]store.Record, string, error) {
	defer rows.Close()
	records := []store.Record{}
	var last referenceCursor
	for rows.Next() {
		var raw []byte
		var at time.Time
		if err := rows.Scan(&raw, &at); err != nil {
			return nil, "", err
		}
		if len(records) == pantryPageSize {
			next, err := s.makeReferenceCursor(last, scope)
			return records, next, err
		}
		record, err := store.DecodeRecord(raw)
		if err != nil {
			return nil, "", err
		}
		records = append(records, record)
		last = referenceCursor{Created: at, ID: store.String(record["id"]), Expires: time.Now().Add(30 * time.Minute).Unix()}
	}
	return records, "", rows.Err()
}
