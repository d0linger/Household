package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const (
	repurchaseWindow   = 64
	repurchaseBudget   = 16 << 20
	repurchaseProducts = 500
	repurchaseDisplay  = 30
)

type RepurchaseSuggestion struct {
	ProductID      string   `json:"product_id"`
	ProductVersion int64    `json:"product_version"`
	Name           string   `json:"name"`
	Brand          string   `json:"brand"`
	PackQuantity   string   `json:"pack_quantity"`
	PackUnit       string   `json:"pack_unit"`
	Dates          []string `json:"dates"`
	Gaps           []int    `json:"gaps"`
	IntervalDays   int      `json:"interval_days"`
	LastOn         string   `json:"last_on"`
	NextOn         string   `json:"next_on"`
	Due            bool     `json:"due"`
	OnList         bool     `json:"on_list"`
	Fingerprint    string   `json:"fingerprint"`
}

type RepurchasePage struct {
	Generation   int64                  `json:"generation"`
	Timezone     string                 `json:"timezone"`
	Today        string                 `json:"today"`
	ListVersion  int64                  `json:"list_version"`
	Checkouts    int                    `json:"checkouts"`
	Window       int                    `json:"window"`
	Insufficient int                    `json:"insufficient"`
	More         bool                   `json:"more"`
	Candidates   []RepurchaseSuggestion `json:"candidates"`
}

type RepurchaseInput struct {
	Generation     int64  `json:"generation"`
	ListVersion    int64  `json:"list_version"`
	ProductID      string `json:"product_id"`
	ProductVersion int64  `json:"product_version"`
	Fingerprint    string `json:"fingerprint"`
	Quantity       string `json:"quantity"`
}

type repurchaseHistory struct {
	Day   string `json:"day"`
	Items []struct {
		ID            string `json:"id"`
		Product       string `json:"product"`
		FrozenProduct string `json:"frozen_product"`
		Pack          string `json:"pack"`
		Unit          string `json:"unit"`
	} `json:"items"`
	Lines []struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
	} `json:"lines"`
}

func repurchaseScope(ctx context.Context, tx pgx.Tx, id auth.Identity, out *RepurchasePage) error {
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) || !value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return ErrForbidden
	}
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT coalesce(ms.data_generation,0),h.timezone,(transaction_timestamp() AT TIME ZONE h.timezone)::date::text,coalesce(ms.state='enabled',false)
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.id=$2 AND m.user_id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=$5
 JOIN core.sessions s ON s.id=$6 AND s.user_id=u.id AND s.auth_version=u.auth_version AND s.revoked_at IS NULL
 AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
 LEFT JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='shopping'
 WHERE h.id=$1`, id.HouseholdID, id.MembershipID, id.UserID, id.Role, id.AuthVersion, id.SessionID).Scan(&out.Generation, &out.Timezone, &out.Today, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err == nil && !enabled {
		return ErrNotFound
	}
	return err
}

func repurchaseList(ctx context.Context, tx pgx.Tx, id auth.Identity, list string, lock bool) (int64, error) {
	query := `SELECT l.version,l.archived_at IS NOT NULL OR l.is_template OR EXISTS(
 SELECT 1 FROM shopping.checkouts c WHERE c.household_id=l.household_id AND c.list_id=l.id)
 FROM shopping.lists l WHERE l.household_id=$1 AND l.id=$2`
	if lock {
		query += " FOR UPDATE OF l NOWAIT"
	}
	var version int64
	var closed bool
	err := tx.QueryRow(ctx, query, id.HouseholdID, list).Scan(&version, &closed)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && closed {
		return 0, ErrNotFound
	}
	return version, shoppingContention(err)
}

// Only bounded scalar Shopping purchase evidence is projected. Receipt,
// price, private notes and Pantry identities never enter this DTO.
const repurchaseHistoryQuery = `SELECT CASE WHEN jsonb_typeof(c.snapshot->'source'->'items')='array'
 AND jsonb_typeof(c.snapshot->'lines')='array' THEN CASE
 WHEN jsonb_array_length(c.snapshot->'source'->'items')=c.item_count AND jsonb_array_length(c.snapshot->'lines')=c.item_count THEN
 jsonb_build_object('day',left(c.snapshot->>'received_on',11),
 'items',(SELECT jsonb_agg(jsonb_build_object('id',left(i->>'id',37),'product',left(coalesce(nullif(i->>'replacement_product_id',''),i->>'product_id'),37),
 'frozen_product',left(i#>>'{product,id}',37),'pack',left(i#>>'{product,pack_quantity}',32),'unit',left(i#>>'{product,pack_unit}',16)))
 FROM jsonb_array_elements(c.snapshot->'source'->'items') i),
 'lines',(SELECT jsonb_agg(jsonb_build_object('id',left(l->>'item_id',37),'decision',left(l->>'decision',16)))
 FROM jsonb_array_elements(c.snapshot->'lines') l)) END END
 FROM shopping.checkouts c WHERE c.household_id=$1 AND c.id=ANY($2::uuid[]) ORDER BY c.id`

func repurchaseEvidence(ctx context.Context, tx pgx.Tx, id auth.Identity, out *RepurchasePage) (map[string]map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT id,history_bytes FROM shopping.checkouts WHERE household_id=$1 ORDER BY created_at DESC,id DESC LIMIT 64`, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, repurchaseWindow)
	var total int64
	for rows.Next() {
		var resource string
		var size int64
		if err = rows.Scan(&resource, &size); err != nil {
			rows.Close()
			return nil, err
		}
		total += size
		if size < 0 || total > repurchaseBudget {
			rows.Close()
			return nil, Invalid{"Die zuletzt erfassten 64 Abschlüsse überschreiten das sichere Auswertungsbudget. Es wurden keine Teilvorschläge berechnet."}
		}
		ids = append(ids, resource)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out.Checkouts = len(ids)
	out.Window = repurchaseWindow
	evidence := map[string]map[string]bool{}
	if len(ids) == 0 {
		return evidence, nil
	}
	rows, err = tx.Query(ctx, repurchaseHistoryQuery, id.HouseholdID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var history repurchaseHistory
		if raw == nil || json.Unmarshal(raw, &history) != nil {
			return nil, Invalid{"Ein gespeicherter Einkaufsabschluss ist nicht eindeutig auswertbar. Es wurden keine Teilvorschläge berechnet."}
		}
		if err = addRepurchaseEvidence(evidence, history, out.Today); err != nil {
			return nil, err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if count != len(ids) {
		return nil, ErrConflict
	}
	return evidence, nil
}

func repurchasePack(product, pack, unit string) (string, error) {
	if !value.ValidUUID(product) {
		return "", ErrConflict
	}
	q, err := value.Decimal(pack)
	if err != nil || q.Sign() <= 0 || !strings.Contains("|piece|g|kg|ml|l|", "|"+unit+"|") {
		return "", ErrConflict
	}
	return strings.ToLower(product) + "/" + q.RatString() + "/" + unit, nil
}

func addRepurchaseEvidence(evidence map[string]map[string]bool, h repurchaseHistory, today string) error {
	day, err := time.Parse(time.DateOnly, h.Day)
	if err != nil || !value.Date(h.Day) || day.Format(time.DateOnly) != h.Day || h.Day > today || len(h.Items) == 0 || len(h.Items) > 500 || len(h.Items) != len(h.Lines) {
		return Invalid{"Kaufdaten oder Artikelzuordnung im Einkaufsverlauf sind uneindeutig. Es wurden keine Teilvorschläge berechnet."}
	}
	items := make(map[string]int, len(h.Items))
	for n, item := range h.Items {
		if !value.ValidUUID(item.ID) {
			return ErrConflict
		}
		itemID := strings.ToLower(item.ID)
		if _, exists := items[itemID]; exists {
			return ErrConflict
		}
		items[itemID] = n
	}
	seen := make(map[string]bool, len(h.Lines))
	for _, line := range h.Lines {
		itemID := strings.ToLower(line.ID)
		n, exists := items[itemID]
		if !exists || seen[itemID] || line.Decision != "skip" && line.Decision != "store" && line.Decision != "no_stock" {
			return ErrConflict
		}
		seen[itemID] = true
		if line.Decision == "skip" {
			continue
		}
		item := h.Items[n]
		if item.Product == "" && item.FrozenProduct == "" {
			continue
		}
		if !strings.EqualFold(item.Product, item.FrozenProduct) {
			return ErrConflict
		}
		key, packErr := repurchasePack(item.Product, item.Pack, item.Unit)
		if packErr != nil {
			return packErr
		}
		if evidence[key] == nil {
			evidence[key] = map[string]bool{}
		}
		evidence[key][h.Day] = true
	}
	return nil
}

func calculateRepurchase(candidate *RepurchaseSuggestion, dates map[string]bool, today string) bool {
	for day := range dates {
		candidate.Dates = append(candidate.Dates, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(candidate.Dates)))
	if len(candidate.Dates) > 8 {
		candidate.Dates = candidate.Dates[:8]
	}
	if len(candidate.Dates) < 3 {
		return false
	}
	for n := 1; n < len(candidate.Dates); n++ {
		a, _ := time.Parse(time.DateOnly, candidate.Dates[n-1])
		b, _ := time.Parse(time.DateOnly, candidate.Dates[n])
		candidate.Gaps = append(candidate.Gaps, int((a.Unix()-b.Unix())/86400))
	}
	ordered := append([]int(nil), candidate.Gaps...)
	sort.Ints(ordered)
	mid := len(ordered) / 2
	candidate.IntervalDays = ordered[mid]
	if len(ordered)%2 == 0 {
		candidate.IntervalDays = (ordered[mid-1] + ordered[mid] + 1) / 2
	}
	candidate.LastOn = candidate.Dates[0]
	last, _ := time.Parse(time.DateOnly, candidate.LastOn)
	candidate.NextOn = last.AddDate(0, 0, candidate.IntervalDays).Format(time.DateOnly)
	candidate.Due = candidate.NextOn <= today
	fingerprint, _ := json.Marshal([]any{candidate.ProductID, candidate.ProductVersion, candidate.PackQuantity, candidate.PackUnit, candidate.Dates})
	sum := sha256.Sum256(fingerprint)
	candidate.Fingerprint = hex.EncodeToString(sum[:])
	return true
}

func (s *Store) repurchasePage(ctx context.Context, tx pgx.Tx, id auth.Identity, list string) (RepurchasePage, error) {
	out := RepurchasePage{Candidates: []RepurchaseSuggestion{}}
	if err := repurchaseScope(ctx, tx, id, &out); err != nil {
		return out, err
	}
	version, err := repurchaseList(ctx, tx, id, list, false)
	if err != nil {
		return out, err
	}
	out.ListVersion = version
	evidence, err := repurchaseEvidence(ctx, tx, id, &out)
	if err != nil {
		return out, err
	}
	ids := map[string]bool{}
	for key := range evidence {
		ids[strings.SplitN(key, "/", 2)[0]] = true
	}
	if len(ids) > repurchaseProducts {
		return out, Invalid{"Mehr als 500 Produktvarianten im begrenzten Verlauf. Es wurden keine Teilvorschläge berechnet."}
	}
	selected := make([]string, 0, len(ids))
	for resource := range ids {
		selected = append(selected, resource)
	}
	if len(selected) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.version,p.name,p.brand,p.pack_quantity::text,p.pack_unit,EXISTS(
 SELECT 1 FROM shopping.items i WHERE i.household_id=p.household_id AND i.list_id=$3 AND coalesce(i.replacement_product_id,i.product_id)=p.id)
 FROM shopping.products p WHERE p.household_id=$1 AND p.id=ANY($2::uuid[]) ORDER BY p.id`, id.HouseholdID, selected, list)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate RepurchaseSuggestion
		if err = rows.Scan(&candidate.ProductID, &candidate.ProductVersion, &candidate.Name, &candidate.Brand, &candidate.PackQuantity, &candidate.PackUnit, &candidate.OnList); err != nil {
			return out, err
		}
		key, packErr := repurchasePack(candidate.ProductID, candidate.PackQuantity, candidate.PackUnit)
		if packErr != nil {
			return out, packErr
		}
		if !calculateRepurchase(&candidate, evidence[key], out.Today) {
			out.Insufficient++
			continue
		}
		out.Candidates = append(out.Candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	sort.Slice(out.Candidates, func(a, b int) bool {
		x, y := out.Candidates[a], out.Candidates[b]
		if x.OnList != y.OnList {
			return !x.OnList
		}
		if x.NextOn != y.NextOn {
			return x.NextOn < y.NextOn
		}
		return x.ProductID < y.ProductID
	})
	return out, nil
}

func (s *Store) ShoppingRepurchase(ctx context.Context, id auth.Identity, list string) (RepurchasePage, error) {
	if !value.ValidUUID(list) {
		return RepurchasePage{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RepurchasePage{}, err
	}
	defer rollback(tx)
	out, err := s.repurchasePage(ctx, tx, id, list)
	if err != nil {
		return out, err
	}
	if len(out.Candidates) > repurchaseDisplay {
		out.More = true
		out.Candidates = out.Candidates[:repurchaseDisplay]
	}
	return out, tx.Commit(ctx)
}

// AddRepurchase is an explicit new shopping item, never a purchase or stock
// mutation. Current authorization and actual list state precede replay delivery.
func (s *Store) AddRepurchase(ctx context.Context, id auth.Identity, list, key, requestID string, in RepurchaseInput) (Record, error) {
	if !value.ValidUUID(list) || !value.ValidUUID(key) || !value.ValidUUID(in.ProductID) || len(in.Fingerprint) != 64 {
		return nil, Invalid{"Vorschlag und Vorgangs-ID erneut prüfen"}
	}
	quantity, err := normalizeRepurchaseQuantity(in.Quantity)
	if err != nil {
		return nil, err
	}
	in.Quantity = quantity
	list = strings.ToLower(list)
	key = strings.ToLower(key)
	in.ProductID = strings.ToLower(in.ProductID)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "shopping", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	version, err := repurchaseList(ctx, tx, id, list, true)
	if err != nil {
		return nil, err
	}
	op := "repurchase/shopping/lists/" + list
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var prior, priorHash []byte
	err = tx.QueryRow(ctx, `SELECT response_payload,request_hash FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4 AND module_id='shopping' AND module_generation=$5`, id.HouseholdID, id.UserID, op, key, in.Generation).Scan(&prior, &priorHash)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		result, decodeErr := decode(prior)
		if decodeErr != nil {
			return nil, decodeErr
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shopping.items i WHERE household_id=$1 AND id=$2 AND list_id=$3 AND product_id=$4)`, id.HouseholdID, result["id"], list, in.ProductID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if version != in.ListVersion {
		return nil, ErrConflict
	}
	var productVersion int64
	if err = tx.QueryRow(ctx, "SELECT version FROM shopping.products WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, in.ProductID).Scan(&productVersion); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, shoppingContention(err)
	}
	if productVersion != in.ProductVersion {
		return nil, ErrConflict
	}
	page, err := s.repurchasePage(ctx, tx, id, list)
	if err != nil {
		return nil, err
	}
	var suggestion *RepurchaseSuggestion
	for n := range page.Candidates {
		if page.Candidates[n].ProductID == in.ProductID {
			suggestion = &page.Candidates[n]
			break
		}
	}
	if suggestion == nil || suggestion.OnList || suggestion.Fingerprint != in.Fingerprint {
		return nil, ErrConflict
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	data := Record{"list_id": list, "name": suggestion.Name, "product_id": in.ProductID, "quantity": quantity, "unit_code": "piece", "availability": "available", "group_name": "", "note": "", "sort_order": int64(0)}
	if err = s.validateDomain(ctx, tx, id, "shopping", "items", "", data, data); err != nil {
		return nil, err
	}
	result, err := save(ctx, tx, id, m, e, value.UUID(), data, true, 0)
	if err != nil {
		return nil, fmt.Errorf("add reviewed repurchase: %w", err)
	}
	if err = s.afterWrite(ctx, tx, id, m, e, result, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "shopping", "items", String(result["id"]), "record_write", requestID); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload) VALUES($1,$2,$3,$4,$5,'shopping',$6,201,$7)`, id.HouseholdID, id.UserID, op, key, hash[:], in.Generation, raw); err != nil {
		return nil, err
	}
	return result, shoppingContention(tx.Commit(ctx))
}

func normalizeRepurchaseQuantity(input string) (string, error) {
	if len(input) > 40 {
		return "", Invalid{"Anzahl Packungen ist zu groß"}
	}
	q, err := value.Decimal(strings.TrimSpace(input))
	if err != nil || q.Sign() <= 0 {
		return "", Invalid{"Positive Anzahl Packungen mit höchstens zwölf Vorkomma- und sechs Nachkommastellen eingeben"}
	}
	quantity := q.FloatString(6)
	exact, err := value.Decimal(quantity)
	if err != nil || exact.Cmp(q) != 0 {
		return "", Invalid{"Anzahl mit höchstens sechs Nachkommastellen eingeben"}
	}
	return quantity, nil
}
