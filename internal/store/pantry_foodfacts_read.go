package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/database"
	"household/internal/foodfacts"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) foodFactsConfiguration() foodfacts.Configuration {
	if s.FoodFacts == nil {
		return foodfacts.Configuration{State: "disabled", Provider: "off-production"}
	}
	configuration := s.FoodFacts.Configuration()
	if !foodFactsProvider(configuration.Provider) || configuration.State != "ready" && configuration.State != "disabled" && configuration.State != "not_configured" {
		return foodfacts.Configuration{State: "not_configured", Provider: "off-production"}
	}
	return configuration
}

func (s *Store) beginFoodFacts(ctx context.Context, id auth.Identity, generation int64) (pgx.Tx, error) {
	if !id.CanWrite() || id.MustChange || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.MembershipID) ||
		!value.ValidUUID(id.SessionID) || id.AuthVersion < 1 {
		return nil, ErrForbidden
	}
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	if err = audit.Bind(ctx, tx); err == nil {
		err = auth.LockSession(ctx, tx, id)
	}
	if err != nil {
		foodFactsRollback(tx)
		if errors.Is(err, auth.ErrSession) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.id=$1 AND m.household_id=$2 AND m.user_id=$3 AND m.role=$4 AND m.inactive_at IS NULL AND NOT u.must_change_password`,
		id.MembershipID, id.HouseholdID, id.UserID, id.Role).Scan(&valid)
	if err != nil || !valid {
		foodFactsRollback(tx)
		if err == nil || errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	var state string
	var currentGeneration int64
	err = tx.QueryRow(ctx, "SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id='pantry' FOR SHARE", id.HouseholdID).Scan(&state, &currentGeneration)
	if err != nil || state != "enabled" || currentGeneration != generation {
		foodFactsRollback(tx)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "enabled" {
			return nil, ErrNotFound
		}
		if err == nil {
			return nil, ErrConflict
		}
		return nil, err
	}
	return tx, nil
}

func foodFactsRollback(tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

// foodFactsReadScope authorizes shared origin metadata without granting a
// reader cache writes or provider access. RR keeps preflight and payload rows
// in one bounded authorized snapshot; row locks serialize authority changes.
func (s *Store) foodFactsReadScope(ctx context.Context, id auth.Identity) (pgx.Tx, int64, error) {
	if id.MustChange || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.MembershipID) ||
		!value.ValidUUID(id.SessionID) || id.AuthVersion < 1 {
		return nil, 0, ErrForbidden
	}
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	if err = audit.Bind(ctx, tx); err == nil {
		err = auth.LockSession(ctx, tx, id)
	}
	if err != nil {
		foodFactsRollback(tx)
		if errors.Is(err, auth.ErrSession) {
			return nil, 0, ErrForbidden
		}
		return nil, 0, shoppingContention(err)
	}
	var generation int64
	err = tx.QueryRow(ctx, `SELECT state.data_generation FROM core.module_states state
 JOIN core.memberships m ON m.household_id=state.household_id AND m.id=$2 AND m.user_id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND NOT u.must_change_password AND u.disabled_at IS NULL
 WHERE state.household_id=$1 AND state.module_id='pantry' AND state.state='enabled' FOR SHARE OF state`,
		id.HouseholdID, id.MembershipID, id.UserID, id.Role).Scan(&generation)
	if err != nil || generation < 1 {
		foodFactsRollback(tx)
		if err == nil || errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, shoppingContention(err)
	}
	return tx, generation, nil
}

func readFoodFactsLocal(ctx context.Context, tx pgx.Tx, id auth.Identity, code string) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('product_id',p.id,'name',p.name,'unit',p.unit_code,
 'archived',p.archived_at IS NOT NULL,'product_version',p.version,'pack_id',b.pack_id,'pack_label',pack.label,
 'pack_disabled',pack.disabled,'pack_version',pack.version)
 FROM pantry.barcodes b JOIN pantry.products p ON p.household_id=b.household_id AND p.id=b.product_id
 LEFT JOIN pantry.packs pack ON pack.household_id=b.household_id AND pack.product_id=b.product_id AND pack.id=b.pack_id
 WHERE b.household_id=$1 AND b.code=$2`, id.HouseholdID, code).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func readFoodFactsCache(ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64, provider, code string) (*foodFactsCache, error) {
	cache := &foodFactsCache{Household: id.HouseholdID, Generation: generation, Provider: provider, Code: code}
	err := tx.QueryRow(ctx, `SELECT state,name,brand,pack_description,error_code,payload_sha256,fingerprint,fetched_at,expires_at,retry_at
 FROM pantry.foodfacts_cache WHERE household_id=$1 AND generation=$2 AND provider=$3 AND code=$4`,
		id.HouseholdID, generation, provider, code).Scan(&cache.State, &cache.Name, &cache.Brand, &cache.Pack,
		&cache.Error, &cache.Payload, &cache.Fingerprint, &cache.FetchedAt, &cache.ExpiresAt, &cache.RetryAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !cache.valid() {
		return nil, ErrConflict
	}
	return cache, nil
}

func foodFactsCachedReply(generation int64, code string, cache *foodFactsCache, now time.Time) PantryFoodFactsLookup {
	out := PantryFoodFactsLookup{Generation: generation, Code: code, State: "unqueried", Message: "Keine lokale Zuordnung. Externen Abruf bei Bedarf ausdrücklich starten."}
	if cache == nil {
		return out
	}
	out.State, out.FromCache, out.Stale = cache.State, true, !cache.ExpiresAt.After(now)
	if cache.State == "found" {
		source := cache.source()
		out.Source = &source
		out.Message = "Unbestätigter Produktvorschlag aus dem lokalen Quellen-Cache. Keine Bestandswirkung."
	} else {
		out.Message = map[string]string{
			"not_found": "Für diesen Code wurden keine Produktdaten gefunden.",
			"error":     "Der Produktdienst lieferte keine verwendbaren Daten. Lokale Erfassung bleibt möglich.",
			"limited":   "Abfragebudget oder Produktdienst begrenzt weitere Abrufe. Später ausdrücklich erneut versuchen.",
		}[cache.State]
	}
	if cache.RetryAt != nil {
		out.RetryAt = cache.RetryAt.UTC().Format(time.RFC3339Nano)
	}
	if out.Stale {
		out.State, out.Message = "stale", "Veralteter Quellenstand. Vor einer Übernahme ausdrücklich neu abrufen; lokale Erfassung bleibt möglich."
	}
	return out
}

func (s *Store) LookupPantryFoodFacts(ctx context.Context, id auth.Identity, in PantryFoodFactsLookupInput) (PantryFoodFactsLookup, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if in.Generation < 1 || len(in.Code) > 256 {
		return PantryFoodFactsLookup{}, Invalid{"Aktuellen Vorratsstand und Code erneut prüfen"}
	}
	code, err := pantryBarcode(in.Code)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	tx, err := s.beginFoodFacts(ctx, id, in.Generation)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	defer foodFactsRollback(tx)
	local, err := readFoodFactsLocal(ctx, tx, id, code)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	out := PantryFoodFactsLookup{Generation: in.Generation, Code: code}
	if local != nil {
		out.State, out.Message, out.Local = "local", "Vorhandene lokale Zuordnung hat Vorrang. Produkt und Packung bleiben unverändert.", local
		return out, tx.Commit(ctx)
	}
	if _, err = foodfacts.ValidateGTIN(code); err != nil {
		out.State, out.Message = "unsupported", "Haushaltscode nur lokal zuordnen. Extern werden ausschließlich geprüfte GTIN-Produktcodes angefragt."
		return out, tx.Commit(ctx)
	}
	configuration := s.foodFactsConfiguration()
	cache, err := readFoodFactsCache(ctx, tx, id, in.Generation, configuration.Provider, code)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return PantryFoodFactsLookup{}, err
	}
	out = foodFactsCachedReply(in.Generation, code, cache, now)
	if configuration.State != "ready" {
		out.State = configuration.State
		out.Message = map[string]string{"disabled": "Open Food Facts ist deaktiviert. Lokale Zuordnung und Erfassung bleiben verfügbar.",
			"not_configured": "Open Food Facts benötigt zuerst einen gültigen eigenen User-Agent in der Betreiberkonfiguration."}[configuration.State]
		return out, tx.Commit(ctx)
	}
	if cache != nil && cache.ExpiresAt.After(now) || !in.AllowExternal {
		return out, tx.Commit(ctx)
	}
	retryAt, err := reserveFoodFactsBudget(ctx, tx)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	if retryAt != nil {
		out.State, out.RetryAt, out.Message = "limited", retryAt.UTC().Format(time.RFC3339Nano), "Das dauerhafte globale Abfragebudget ist belegt. Später ausdrücklich erneut versuchen."
		return out, tx.Commit(ctx)
	}
	if err = tx.Commit(ctx); err != nil {
		return PantryFoodFactsLookup{}, err
	}
	// No transaction, session row lock or household barrier is held during HTTP.
	result, callErr := s.FoodFacts.Lookup(ctx, code)
	if callErr != nil {
		result = foodfacts.Result{State: "error", ErrorCode: "provider_error"}
	}
	if result.State == "limited" {
		if err = s.recordFoodFactsCooldown(ctx, result.RetryAfter); err != nil {
			return PantryFoodFactsLookup{}, err
		}
	}
	post, err := s.beginFoodFacts(ctx, id, in.Generation)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	defer foodFactsRollback(post)
	local, err = readFoodFactsLocal(ctx, post, id, code)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	if local != nil {
		out.State, out.Local, out.Source, out.Stale = "local", local, nil, false
		out.Message = "Inzwischen wurde eine lokale Zuordnung angelegt. Diese hat Vorrang; externe Werte wurden nicht übernommen."
		return out, post.Commit(ctx)
	}
	current := s.foodFactsConfiguration()
	if current != configuration {
		return PantryFoodFactsLookup{}, ErrConflict
	}
	written, err := writeFoodFactsCache(ctx, post, id, in.Generation, configuration.Provider, code, result)
	if err != nil {
		return PantryFoodFactsLookup{}, err
	}
	out = foodFactsCachedReply(in.Generation, code, written, written.FetchedAt)
	out.FromCache = false
	if result.State != "found" && cache != nil && cache.State == "found" {
		source := cache.source()
		out.Source, out.Stale = &source, true
		out.Message += " Vorherige Angaben sind veraltet und nicht übernehmbar."
	}
	return out, post.Commit(ctx)
}

var foodFactsBudgetSubject = sha256.Sum256([]byte("household-installation-foodfacts-v1"))

func lockFoodFactsBudget(ctx context.Context, tx pgx.Tx) error {
	var acquired bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended('pantry-foodfacts-budget-v1',0))").Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrConflict
	}
	return nil
}

func reserveFoodFactsBudget(ctx context.Context, tx pgx.Tx) (*time.Time, error) {
	if err := lockFoodFactsBudget(ctx, tx); err != nil {
		return nil, err
	}
	var count int64
	var earliest, cooldown *time.Time
	err := tx.QueryRow(ctx, `SELECT coalesce(sum(attempt_count),0),min(window_end)
 FROM core.rate_limit_buckets WHERE scope='pantry-off-product-v1' AND subject_hash=$1 AND window_end>clock_timestamp()`, foodFactsBudgetSubject[:]).Scan(&count, &earliest)
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT max(window_end) FROM core.rate_limit_buckets
 WHERE scope='pantry-off-cooldown-v1' AND subject_hash=$1 AND window_end>clock_timestamp()`, foodFactsBudgetSubject[:]).Scan(&cooldown); err != nil {
		return nil, err
	}
	if cooldown != nil {
		return cooldown, nil
	}
	if count >= 12 {
		return earliest, nil
	}
	_, err = tx.Exec(ctx, `WITH moment AS(SELECT date_trunc('second',clock_timestamp()) AS at)
 INSERT INTO core.rate_limit_buckets(scope,subject_hash,window_start,window_end,attempt_count)
 SELECT 'pantry-off-product-v1',$1,at,at+interval '61 seconds',1 FROM moment
 ON CONFLICT(scope,subject_hash,window_start) DO UPDATE SET attempt_count=core.rate_limit_buckets.attempt_count+1,updated_at=clock_timestamp()`, foodFactsBudgetSubject[:])
	return nil, err
}

func (s *Store) recordFoodFactsCooldown(ctx context.Context, duration time.Duration) error {
	if duration < time.Second {
		duration = time.Minute
	}
	if duration > 24*time.Hour {
		duration = 24 * time.Hour
	}
	// Rate infrastructure is independent of domain authority. A received429 must
	// protect the installation even when the requesting session was just revoked.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "system"), s.Pool)
	if err != nil {
		return err
	}
	defer foodFactsRollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pantry-foodfacts-budget-v1',0))"); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.rate_limit_buckets(scope,subject_hash,window_start,window_end,attempt_count)
 VALUES('pantry-off-cooldown-v1',$1,'2000-01-01T00:00:00Z',clock_timestamp()+make_interval(secs=>$2),1)
 ON CONFLICT(scope,subject_hash,window_start) DO UPDATE SET window_end=greatest(core.rate_limit_buckets.window_end,excluded.window_end),updated_at=clock_timestamp()`,
		foodFactsBudgetSubject[:], int64((duration+time.Second-1)/time.Second))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeFoodFactsCache(ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64, provider, code string, result foodfacts.Result) (*foodFactsCache, error) {
	var acquired bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", pantryFoodFactsNamespace+"/cache/"+id.HouseholdID).Scan(&acquired); err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrConflict
	}
	cache := &foodFactsCache{Household: id.HouseholdID, Generation: generation, Provider: provider, Code: code, State: result.State, Error: result.ErrorCode}
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&cache.FetchedAt); err != nil {
		return nil, err
	}
	cache.ExpiresAt = cache.FetchedAt.Add(time.Minute)
	if result.PayloadSHA256 != "" {
		var err error
		cache.Payload, err = hex.DecodeString(result.PayloadSHA256)
		if err != nil || len(cache.Payload) != 32 {
			return nil, ErrConflict
		}
	}
	switch result.State {
	case "found":
		if result.Product == nil || result.Product.Code != code {
			return nil, ErrConflict
		}
		cache.Name, cache.Brand, cache.Pack = result.Product.Name, result.Product.Brand, result.Product.PackDescription
		cache.ExpiresAt = cache.FetchedAt.Add(7 * 24 * time.Hour)
	case "not_found":
		cache.ExpiresAt = cache.FetchedAt.Add(time.Hour)
	case "limited":
		duration := result.RetryAfter
		if duration < time.Second {
			duration = time.Minute
		}
		if duration > 24*time.Hour {
			duration = 24 * time.Hour
		}
		retry := cache.FetchedAt.Add(duration)
		var global *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(window_end) FROM core.rate_limit_buckets
 WHERE scope='pantry-off-cooldown-v1' AND subject_hash=$1 AND window_end>clock_timestamp()`, foodFactsBudgetSubject[:]).Scan(&global); err != nil {
			return nil, err
		}
		if global != nil && global.After(retry) {
			retry = *global
		}
		cache.RetryAt, cache.ExpiresAt = &retry, retry
	case "error":
	default:
		return nil, ErrConflict
	}
	var err error
	cache.Fingerprint, err = cache.digest()
	if err != nil || !cache.valid() {
		return nil, ErrConflict
	}
	var count int
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_or(provider=$3 AND code=$4),false)
 FROM pantry.foodfacts_cache WHERE household_id=$1 AND generation=$2`, id.HouseholdID, generation, provider, code).Scan(&count, &exists); err != nil {
		return nil, err
	}
	if count > 1000 {
		return nil, Invalid{"Quellen-Cache überschreitet seine zulässige Größe"}
	}
	if count == 1000 && !exists {
		if _, err = tx.Exec(ctx, `DELETE FROM pantry.foodfacts_cache WHERE(household_id,generation,provider,code) IN
 (SELECT household_id,generation,provider,code FROM pantry.foodfacts_cache WHERE household_id=$1 AND generation=$2 ORDER BY fetched_at,provider,code LIMIT 1)`, id.HouseholdID, generation); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO pantry.foodfacts_cache(household_id,generation,provider,code,state,name,brand,pack_description,error_code,payload_sha256,fingerprint,fetched_at,expires_at,retry_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
 ON CONFLICT(household_id,generation,provider,code) DO UPDATE SET state=excluded.state,name=excluded.name,brand=excluded.brand,pack_description=excluded.pack_description,
 error_code=excluded.error_code,payload_sha256=excluded.payload_sha256,fingerprint=excluded.fingerprint,fetched_at=excluded.fetched_at,expires_at=excluded.expires_at,retry_at=excluded.retry_at`,
		id.HouseholdID, generation, provider, code, cache.State, cache.Name, cache.Brand, cache.Pack, cache.Error,
		cache.Payload, cache.Fingerprint, cache.FetchedAt, cache.ExpiresAt, cache.RetryAt)
	return cache, err
}

type foodFactsAdoptedText struct {
	ProductName       string `json:"product_name"`
	Unit              string `json:"unit"`
	IntegerQuantities bool   `json:"integer_quantities"`
	PackLabel         string `json:"pack_label"`
	PackQuantity      string `json:"pack_quantity"`
	PackUnit          string `json:"pack_unit"`
}

type foodFactsOriginSnapshot struct {
	Source  PantryFoodFactsSource `json:"source"`
	Adopted foodFactsAdoptedText  `json:"adopted"`
}

type foodFactsExportOrigin struct {
	foodFactsOriginSnapshot
	SelectedFields []string `json:"selected_fields"`
}

func validFoodFactsOrigin(origin foodFactsOriginSnapshot, selected []string) bool {
	if !validFoodFactsSource(origin.Source) || origin.Adopted.ProductName == "" || !foodFactsText(origin.Adopted.ProductName, 200) ||
		!foodFactsUnit(origin.Adopted.Unit) || len(selected) < 1 || len(selected) > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, field := range selected {
		if seen[field] || map[string]string{"name": origin.Source.Name, "brand": origin.Source.Brand, "pack_description": origin.Source.PackDescription}[field] == "" {
			return false
		}
		seen[field] = true
	}
	pack := origin.Adopted
	if pack.PackLabel == "" && pack.PackQuantity == "" && pack.PackUnit == "" {
		return true
	}
	quantity, err := value.Decimal(pack.PackQuantity)
	return err == nil && quantity.Sign() > 0 && quantity.FloatString(6) == pack.PackQuantity && pack.PackLabel != "" &&
		foodFactsText(pack.PackLabel, 100) && foodFactsUnit(pack.PackUnit)
}

// PantryFoodFactsExportSources returns complete, explicitly bounded attribution
// for the currently authorized export page. It never fetches remote data.
func (s *Store) PantryFoodFactsExportSources(ctx context.Context, id auth.Identity, entity string, ids []string) (map[string]string, error) {
	column := map[string]string{"products": "product_id", "packs": "pack_id", "barcodes": "barcode_id"}[entity]
	if column == "" || len(ids) > 100 {
		return nil, Invalid{"Produktquellen nur für höchstens hundert erlaubte Vorratsdatensätze gemeinsam exportieren"}
	}
	ids = append([]string(nil), ids...)
	result := make(map[string]string, len(ids))
	for index, record := range ids {
		if !value.ValidUUID(record) {
			return nil, Invalid{"Gültige Datensatzkennungen für den Quellenexport erforderlich"}
		}
		record = strings.ToLower(record)
		if _, duplicate := result[record]; duplicate {
			return nil, Invalid{"Datensatzkennungen für den Quellenexport müssen eindeutig sein"}
		}
		ids[index], result[record] = record, "[]"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, generation, err := s.foodFactsReadScope(ctx, id)
	if err != nil {
		return nil, err
	}
	defer foodFactsRollback(tx)
	var schemaRows, schemaVersion int64
	var dirty bool
	if err = tx.QueryRow(ctx, "SELECT count(*),coalesce(max(version),0),coalesce(bool_or(dirty),false) FROM pantry.schema_migrations").Scan(&schemaRows, &schemaVersion, &dirty); err != nil {
		return nil, err
	}
	if schemaRows != 1 || dirty || schemaVersion < 1 || schemaVersion > int64(database.ExpectedVersion("pantry")) {
		return nil, ErrConflict
	}
	if schemaVersion < 12 {
		// Genuine predecessor exports have no PA13 provenance. The explicit
		// clean catalog version proof is required before this compatibility path.
		return result, tx.Commit(ctx)
	}
	var ready bool
	if err = tx.QueryRow(ctx, "SELECT pantry.prepared_capture_ready()").Scan(&ready); err != nil {
		return nil, err
	}
	if !ready {
		return nil, ErrConflict
	}
	// Identifiers are selected only from this compiled three-entity whitelist.
	from := " FROM pantry." + pgx.Identifier{entity}.Sanitize() + " r JOIN pantry.foodfacts_provenance p ON p.household_id=r.household_id AND p." + pgx.Identifier{column}.Sanitize() + "=r.id WHERE r.household_id=$1 AND r.id=ANY($2::uuid[]) AND p.generation=$3"
	var count int64
	var estimated int64
	err = tx.QueryRow(ctx, "SELECT count(*),coalesce(sum(bytes),0) FROM(SELECT octet_length(p.source_snapshot::text)+512 AS bytes"+from+" LIMIT 1001) bounded", id.HouseholdID, ids, generation).Scan(&count, &estimated)
	if err != nil {
		return nil, err
	}
	if count > 1000 || estimated > 2<<20 {
		return nil, Invalid{"Quellenexport überschreitet tausend Herkunftsbelege oder 2 MiB pro Seite; Exportumfang eingrenzen"}
	}
	rows, err := tx.Query(ctx, "SELECT r.id,p.source_snapshot::text,p.selected_fields"+from+" ORDER BY r.id,p.created_at,p.id LIMIT 1001", id.HouseholdID, ids, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make(map[string][]foodFactsExportOrigin, len(ids))
	total, facts := 0, 0
	for rows.Next() {
		var record string
		var raw []byte
		var fields []string
		if err = rows.Scan(&record, &raw, &fields); err != nil {
			return nil, err
		}
		var origin foodFactsOriginSnapshot
		if foodFactsDecode(raw, &origin, 8192) != nil || !validFoodFactsOrigin(origin, fields) {
			return nil, ErrConflict
		}
		facts++
		total += len(raw) + 512
		if facts > 1000 || total > 2<<20 {
			return nil, Invalid{"Quellenexport überschreitet die vollständige Seitengrenze; Exportumfang eingrenzen"}
		}
		groups[record] = append(groups[record], foodFactsExportOrigin{foodFactsOriginSnapshot: origin, SelectedFields: fields})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	total = 0
	for record, origins := range groups {
		raw, err := json.Marshal(origins)
		if err != nil {
			return nil, err
		}
		total += len(raw)
		if len(raw) > 256<<10 || total > 2<<20 {
			return nil, Invalid{"Vollständige Herkunft für einen Datensatz überschreitet 256 KiB oder die Seitengrenze; Exportumfang eingrenzen"}
		}
		result[record] = string(raw)
	}
	return result, tx.Commit(ctx)
}
