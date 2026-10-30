//go:build integration

package web

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func pantryExtensionsPriorCatalog(t *testing.T, f *integrationFixture, pantryVersion uint) {
	t.Helper()
	if pantryVersion != 10 && pantryVersion != 11 {
		t.Fatal("only genuine Pantry10/Pantry11 predecessor fixtures are supported")
	}
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		switch schema {
		case "core":
			version = 26
		case "fuel":
			version = 5
		case "recipes":
			version = 4
		case "mealplan":
			version = 7 // Mealplan8 requires the later Core28 extension stage.
		case "pantry":
			version = pantryVersion
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	// Actual complete predecessor migrations create the global policy census;
	// never rebase Pantry12's strict predecessor onto a dropped-module remnant.
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal(err)
	}
	var version int
	var dirty, ready bool
	if err := f.pool.QueryRow(f.ctx, "SELECT version,dirty,core.audit_ready() FROM pantry.schema_migrations").Scan(&version, &dirty, &ready); err != nil || version != int(pantryVersion) || dirty || !ready {
		t.Fatal("actual compatible Core26/Shopping8/Fuel5/Pantry predecessor required", version, dirty, ready, err)
	}
	ids, enabled := []string{}, map[string]bool{}
	for _, descriptor := range f.app.Store.Modules {
		ids = append(ids, descriptor.ID)
		enabled[descriptor.ID] = descriptor.DefaultEnabled
	}
	if err := f.app.Auth.Bootstrap(f.ctx, ids, enabled); err != nil {
		t.Fatal(err)
	}
	// Fixture setup only; no execution of an old compiled application is claimed.
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.users SET must_change_password=false WHERE username=$1", f.app.Cfg.AdminUsername); err != nil {
		t.Fatal(err)
	}
	var err error
	f.token, f.identity, err = f.app.Auth.Login(f.ctx, f.app.Cfg.AdminUsername, integrationPassword, "", "127.0.0.1", "Pantry predecessor fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func pantryExtensionsCatalog(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var result string
	if err := f.owner.QueryRow(f.ctx, "SELECT encode(sha256(convert_to(pantry.prepared_capture_catalog()::text,'UTF8')),'hex')").Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func pantryExtensionsPriorFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var result string
	err := f.owner.QueryRow(f.ctx, `SELECT encode(sha256(convert_to(jsonb_build_object(
 'products',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM pantry.products p WHERE household_id=$1),
 'packs',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM pantry.packs p WHERE household_id=$1),
 'barcodes',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.id) FROM pantry.barcodes b WHERE household_id=$1),
 'locations',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id) FROM pantry.locations l WHERE household_id=$1),
 'lots',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id) FROM pantry.lots l WHERE household_id=$1),
 'journal',(SELECT jsonb_agg(to_jsonb(m) ORDER BY m.id) FROM pantry.stock_movements m WHERE household_id=$1),
 'balances',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.lot_id) FROM pantry.lot_balances b WHERE household_id=$1)
 )::text,'UTF8')),'hex')`, f.identity.HouseholdID).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPantryExtensionsPopulatedPantry11UpgradePreservesExactFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	pantryExtensionsPriorCatalog(t, f, 11)
	product, lot := pantryFixture(t, f, "real Pantry11 upgrade")
	pack, _ := auditCreate(t, f, "pantry", "packs", store.Record{"product_id": product["id"], "label": "Original pack", "quantity": "0.125001", "unit_code": "kg"})
	auditCreate(t, f, "pantry", "barcodes", store.Record{"product_id": product["id"], "pack_id": pack["id"], "code": "PANTRY11-ORIGINAL"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "9007199254.740993"})
	destination, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Original transfer destination"})
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("lots")
	mutation := auditMutation(t, f, "pantry", store.Record{"location_id": destination["id"], "quantity": "0.125001"}, store.Int(lot["version"]))
	if _, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(lot["id"]), "transfer", value.UUID(), value.UUID(), mutation); err != nil {
		t.Fatal("actual prior-schema ordinary transfer", err)
	}
	before := pantryExtensionsPriorFacts(t, f)
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("actual forward migration/repeat", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal(err)
		}
		if pantryExtensionsPriorFacts(t, f) != before {
			t.Fatal("forward migration rewrote original exact facts/journal/balances")
		}
	}
	var generated int
	var serving bool
	err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pantry.leftover_operations)+(SELECT count(*) FROM pantry.prepared_lots)+
 (SELECT count(*) FROM pantry.prepared_transfers)+(SELECT count(*) FROM pantry.foodfacts_cache)+
 (SELECT count(*) FROM pantry.foodfacts_operations)+(SELECT count(*) FROM pantry.foodfacts_provenance),
 EXISTS(SELECT 1 FROM core.units WHERE code='portion' AND dimension='serving' AND factor_to_base=1)`).Scan(&generated, &serving)
	if err != nil || generated != 0 || !serving {
		t.Fatal("upgrade invented prepared/source facts or omitted independent serving dimension", generated, serving, err)
	}
}

func TestPantryExtensionsReadinessRejectsGuardCatalogAndPrivilegeDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	before := pantryExtensionsCatalog(t, f)
	for _, test := range []struct{ name, sql string }{
		{"dish bound", "ALTER TABLE pantry.prepared_lots DROP CONSTRAINT prepared_lots_dish_name_check"},
		{"private receipt", "GRANT SELECT ON pantry.leftover_operations TO household_app"},
		{"private transfer insertion", "GRANT INSERT ON pantry.prepared_transfers TO household_app"},
		{"private source boundary", "GRANT EXECUTE ON FUNCTION core.pantry_leftover_source(uuid,text,uuid,bigint,uuid) TO household_app"},
		{"transfer guard", "ALTER TABLE pantry.prepared_transfers DISABLE TRIGGER prepared_transfer_guard"},
		{"transfer deferred completion", "ALTER TABLE pantry.prepared_transfers DISABLE TRIGGER prepared_transfer_complete"},
		{"source index", "DROP INDEX pantry.prepared_transfer_root"},
		{"redacted provenance policy", "DELETE FROM core.audit_policies WHERE schema_name='pantry' AND table_name='foodfacts_provenance'"},
		{"cache collation", "ALTER TABLE pantry.foodfacts_cache ALTER COLUMN code TYPE text COLLATE \"default\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tx.Rollback(cleanup)
			}()
			if _, err = tx.Exec(f.ctx, test.sql); err != nil {
				t.Fatal("install explicit rollback-only catalog fault", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT pantry.prepared_capture_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("current contract accepted actual uncommitted catalog drift", ready, err)
			}
		})
		if pantryExtensionsCatalog(t, f) != before {
			t.Fatal("rollback-only readiness probe changed the actual catalog")
		}
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness was not restored after bounded probes", err)
	}
}

func pantryExtensionsRetained(t *testing.T, f *integrationFixture, generation int64) (string, [2]int64) {
	t.Helper()
	var result string
	var counts [2]int64
	err := f.owner.QueryRow(f.ctx, `SELECT encode(sha256(convert_to(jsonb_build_object(
 'leftovers',(SELECT jsonb_agg(to_jsonb(o)-ARRAY['result','result_sha256','source_cap','source_prepared_on'] ORDER BY o.id)
 FROM pantry.leftover_operations o WHERE household_id=$1 AND generation=$2),
 'foodfacts',(SELECT jsonb_agg(to_jsonb(o)-ARRAY['result','result_hash'] ORDER BY o.id)
 FROM pantry.foodfacts_operations o WHERE household_id=$1 AND generation=$2))::text,'UTF8')),'hex'),
 (SELECT count(*) FROM pantry.leftover_operations WHERE household_id=$1 AND generation=$2),
 (SELECT count(*) FROM pantry.foodfacts_operations WHERE household_id=$1 AND generation=$2)`, f.identity.HouseholdID, generation).Scan(&result, &counts[0], &counts[1])
	if err != nil {
		t.Fatal(err)
	}
	return result, counts
}

func pantryExtensionsBudget(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var result string
	if err := f.owner.QueryRow(f.ctx, `SELECT encode(sha256(convert_to(coalesce(jsonb_agg(to_jsonb(b) ORDER BY scope,window_start),'[]')::text,'UTF8')),'hex')
 FROM core.rate_limit_buckets b WHERE scope IN('pantry-off-product-v1','pantry-off-cooldown-v1')`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPantryExtensionsActualPurgeRetainsOnlyIntentFences(t *testing.T) {
	f := newIntegrationFixture(t)
	_, cooking, _ := cookingStockFixture(t, f)
	manual := pantryLeftoverReview(t, f, pantryLeftoverFixture(t, f))
	manualKey := value.UUID()
	if _, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, manualKey, value.UUID(), manual); err != nil {
		t.Fatal(err)
	}
	source, err := f.app.Store.PantryLeftoversContext(f.ctx, f.identity, "cooking", cooking)
	if err != nil {
		t.Fatal(err)
	}
	cooked := manual
	cooked.Containers = append([]store.PantryLeftoverContainer(nil), manual.Containers...)
	cooked.SourceKind, cooked.SourceID, cooked.RecipesGeneration = "cooking", cooking, store.Int(source["recipes_generation"])
	cooked.Name, cooked.Fingerprint, cooked.Containers[0].ID, cooked.Containers[0].Quantity = "SYNTHETIC cooked leftovers", "", value.UUID(), "0.5"
	cooked = pantryLeftoverReview(t, f, cooked)
	cookedKey := value.UUID()
	receipt, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, cookedKey, value.UUID(), cooked)
	if err != nil {
		t.Fatal(err)
	}
	root := store.String(receipt["lines"].([]store.Record)[0]["lot_id"])
	destination, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "SYNTHETIC purge transfer"})
	transferred := pantryLeftoverTransfer(t, f, root, store.String(destination["id"]), "0.25")
	provider := pantryFoodFactsProvider(f) // Deterministic test provider, not a real OFF availability claim.
	foodInput, foodKey := pantryFoodFactsInput(t, f, "4006381333931"), value.UUID()
	foodReview, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, foodKey, foodInput)
	if err != nil {
		t.Fatal(err)
	}
	food, err := f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, foodKey, value.UUID(), foodReview.Input, foodReview.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	generation := manual.Generation
	if _, err = f.app.Store.AbandonPantryLeftoverOperation(f.ctx, f.identity, value.UUID(), generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.AbandonPantryFoodFacts(f.ctx, f.identity, value.UUID(), generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	provider.limited = true
	limited, err := f.app.Store.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: "012345678905", AllowExternal: true})
	if err != nil || limited.State != "limited" || provider.calls.Load() != 2 {
		t.Fatal("actual persistent provider cooldown required", limited, err)
	}
	provider.limited = false
	budget, catalog := pantryExtensionsBudget(t, f), pantryExtensionsCatalog(t, f)
	retained, counts := pantryExtensionsRetained(t, f, generation)
	if counts != [2]int64{3, 2} {
		t.Fatal("genuine accepted and refused intent fences are required", counts)
	}
	recipeState := f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, recipeState.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.PantryPreparedLot(f.ctx, f.identity, transferred)
	if err != nil || view["prepared"].(store.Record)["dish_name"] != cooked.Name || view["source"].(store.Record)["available"] != false {
		t.Fatal("disabled optional source erased owned transferred preparation or leaked live details", view, err)
	}
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, cookedKey, value.UUID(), cooked); err != nil {
		t.Fatal("historical receipt improperly required live Recipes", err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", true, f.app.Store.States()["recipes"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, request := value.UUID(), value.UUID()
	// Synthetic verified-backup-eligibility metadata ONLY. The real encrypted
	// backup/restore rehearsal is a separate parent-operated acceptance gate.
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'SYNTHETIC Pantry lifecycle eligibility','not-a-real-backup',$2)`, backup, "synthetic-eligibility-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,'pantry',$3,$4,$5)`, request, f.identity.HouseholdID, generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	beforeWrongRole := pantryFoodFactsEffects(t, f)
	if err = maintenance.Purge(f.ctx, f.pool, request); err == nil || !strings.Contains(err.Error(), "separate maintenance role") || pantryFoodFactsEffects(t, f) != beforeWrongRole {
		t.Fatal("runtime database role performed or changed an operator purge", err)
	}
	if err = maintenance.Purge(f.ctx, f.owner, request); err != nil {
		t.Fatal("actual owner Pantry purge", err)
	}
	if after, actual := pantryExtensionsRetained(t, f, generation); after != retained || actual != counts {
		t.Fatal("purge removed or rewrote minimal accepted/refused fences", actual, counts)
	}
	var payloads, facts, currentGeneration int64
	var state, requestState string
	err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM pantry.leftover_operations WHERE household_id=$1 AND(result IS NOT NULL OR result_sha256 IS NOT NULL OR source_cap IS NOT NULL OR source_prepared_on IS NOT NULL))+
 (SELECT count(*) FROM pantry.foodfacts_operations WHERE household_id=$1 AND(result IS NOT NULL OR result_hash IS NOT NULL)),
 (SELECT count(*) FROM pantry.products)+(SELECT count(*) FROM pantry.packs)+(SELECT count(*) FROM pantry.barcodes)+(SELECT count(*) FROM pantry.locations)+
 (SELECT count(*) FROM pantry.lots)+(SELECT count(*) FROM pantry.stock_movements)+(SELECT count(*) FROM pantry.prepared_lots)+
 (SELECT count(*) FROM pantry.prepared_transfers)+(SELECT count(*) FROM pantry.foodfacts_cache)+(SELECT count(*) FROM pantry.foodfacts_provenance),
 s.data_generation,s.state,p.state FROM core.module_states s JOIN core.module_purge_requests p ON p.id=$2
 WHERE s.household_id=$1 AND s.module_id='pantry'`, f.identity.HouseholdID, request).Scan(&payloads, &facts, &currentGeneration, &state, &requestState)
	if err != nil || payloads != 0 || facts != 0 || currentGeneration != generation+1 || state != "disabled" || requestState != "completed" {
		t.Fatal("purge retained facts/response details or failed its generation boundary", payloads, facts, currentGeneration, state, requestState, err)
	}
	if pantryExtensionsCatalog(t, f) != catalog || pantryExtensionsBudget(t, f) != budget {
		t.Fatal("purge changed schema proof or erased independent Core provider budget")
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", true, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	beforeReplay := pantryFoodFactsEffects(t, f)
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, manualKey, value.UUID(), manual); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old generation leftover recreated stock", err)
	}
	if _, err = f.app.Store.PantryLeftoverOperation(f.ctx, f.identity, cookedKey, generation); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old generation status returned a purged receipt", err)
	}
	if status, statusErr := f.app.Store.PantryLeftoverOperation(f.ctx, f.identity, cookedKey, currentGeneration); statusErr != nil || status["state"] != "unknown" {
		t.Fatal("current generation inferred old acceptance", status, statusErr)
	}
	if _, err = f.app.Store.PantryFoodFactsOperationStatus(f.ctx, f.identity, foodKey, generation); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old food-facts generation disclosed a response", err)
	}
	if status, statusErr := f.app.Store.PantryFoodFactsOperationStatus(f.ctx, f.identity, foodKey, currentGeneration); statusErr != nil || status.State != "unknown" || status.Receipt != nil {
		t.Fatal("current generation reconstructed a purged catalog receipt", status, statusErr)
	}
	newFood := foodReview.Input
	newFood.Generation = currentGeneration
	if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, foodKey, value.UUID(), newFood, foodReview.Fingerprint); !errors.Is(err, store.ErrConflict) {
		t.Fatal("client generation update revived a permanent food-facts key", err)
	}
	if _, err = f.app.Store.PantryPreparedLot(f.ctx, f.identity, transferred); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("purged transferred detail survived", err)
	}
	if _, err = f.app.Store.PantryFoodFactsProvenance(f.ctx, f.identity, food.ProductID, currentGeneration); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("purged product provenance survived", err)
	}
	if pantryFoodFactsEffects(t, f) != beforeReplay {
		t.Fatal("refused old operations recreated facts/audits/outbox")
	}
	fresh := pantryLeftoverFixture(t, f)
	reusedCooking := cooked
	reusedCooking.Generation = currentGeneration
	reusedCooking.Containers = append([]store.PantryLeftoverContainer(nil), cooked.Containers...)
	reusedCooking.Containers[0].LocationID = fresh.Containers[0].LocationID
	reusedCooking.Containers[0].LocationVersion = fresh.Containers[0].LocationVersion
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), reusedCooking); err == nil {
		t.Fatal("new Pantry generation/key duplicated retained cooking source")
	} else {
		var sqlError *pgconn.PgError
		if !errors.As(err, &sqlError) || sqlError.Code != "23514" || !strings.Contains(sqlError.Message, "cooking source already recorded") {
			t.Fatal("old cooking source was refused for an unrelated fixture error", err)
		}
	}
	fresh = pantryLeftoverReview(t, f, fresh)
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), fresh); err != nil {
		t.Fatal("fresh explicit next-generation preparation", err)
	}
	limited, err = f.app.Store.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: currentGeneration, Code: pantryFoodFactsGTIN(30), AllowExternal: true})
	if err != nil || limited.State != "limited" || provider.calls.Load() != 2 || pantryExtensionsBudget(t, f) != budget {
		t.Fatal("Purge/current generation bypassed the persistent installation cooldown", limited, err)
	}
	if after, actual := pantryExtensionsRetained(t, f, generation); after != retained || actual != counts {
		t.Fatal("fresh writes altered old retained intent fences", actual)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

// This is a native ACL/default-equivalence and fail-closed regression.
// Actual pg_dump/pg_restore acceptance remains a separate guarded operator run.
func TestPantryExtensionsEffectiveTableACLDefaultsAndPrivilegeDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	before := pantryExtensionsCatalog(t, f)
	var installedBody, capturedOwnerACL string
	if err := f.owner.QueryRow(f.ctx, `SELECT pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure),
 (SELECT entry->>8 FROM jsonb_array_elements(pantry.prepared_capture_catalog()->'tables') entry
 WHERE entry->>0='leftover_operations')`).Scan(&installedBody, &capturedOwnerACL); err != nil {
		t.Fatal("read the installed canonical private table ACL contract", err)
	}
	// The actual NULL representation is witnessed through a new temporary table,
	// without superuser catalog writes or an external-tool dependency in Go tests.
	if !strings.Contains(installedBody, "coalesce(c.relacl,pg_catalog.acldefault('r',c.relowner))::text") {
		t.Fatal("installed table ACL capture omits the approved default-ACL fallback")
	}
	var runtimeIsNotOwner, privateSelectDenied bool
	if err := f.pool.QueryRow(f.ctx, `SELECT current_user='household_app'
 AND NOT pg_has_role(current_user,'household_owner','MEMBER'),
 NOT has_table_privilege(current_user,'pantry.leftover_operations','SELECT')`).Scan(&runtimeIsNotOwner, &privateSelectDenied); err != nil || !runtimeIsNotOwner || !privateSelectDenied {
		t.Fatal("runtime role must retain its distinct non-owner/private-table boundary", err)
	}
	if _, err := f.pool.Exec(f.ctx, "SELECT pantry.prepared_capture_catalog()"); err == nil {
		t.Fatal("runtime role inspected private captured catalog details")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatal("private catalog refusal had an unrelated cause", err)
		}
	}
	assertRestored := func(t *testing.T) {
		t.Helper()
		var body string
		if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure)").Scan(&body); err != nil {
			t.Fatal(err)
		}
		if body != installedBody || pantryExtensionsCatalog(t, f) != before {
			t.Fatal("rollback changed the original body, ACL or remaining catalog")
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("strict complete runtime readiness was not restored", err)
		}
	}
	t.Run("real null default and explicit owner ACL have identical canonical rights", func(t *testing.T) {
		tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = tx.Rollback(cleanup)
		}()
		if _, err = tx.Exec(f.ctx, "CREATE TEMP TABLE pantry_acl_equivalence(id uuid) ON COMMIT DROP"); err != nil {
			t.Fatal(err)
		}
		var nullACL bool
		var owner, canonical string
		if err = tx.QueryRow(f.ctx, `SELECT c.relacl IS NULL,pg_get_userbyid(c.relowner),
 coalesce(c.relacl,pg_catalog.acldefault('r',c.relowner))::text
 FROM pg_class c WHERE c.oid='pg_temp.pantry_acl_equivalence'::regclass`).Scan(&nullACL, &owner, &canonical); err != nil || !nullACL || owner != "household_owner" || canonical != capturedOwnerACL {
			t.Fatal("actual default table ACL differs from the captured owner-only rights", nullACL, owner, err)
		}
		if _, err = tx.Exec(f.ctx, "GRANT ALL PRIVILEGES ON TABLE pg_temp.pantry_acl_equivalence TO household_owner"); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(f.ctx, `SELECT c.relacl IS NULL,
 coalesce(c.relacl,pg_catalog.acldefault('r',c.relowner))::text
 FROM pg_class c WHERE c.oid='pg_temp.pantry_acl_equivalence'::regclass`).Scan(&nullACL, &canonical); err != nil || nullACL || canonical != capturedOwnerACL {
			t.Fatal("explicit identical owner-only grant changed canonical rights", nullACL, err)
		}
		// An existing protected table's identical owner grant is also a no-op in
		// the complete contract; no private App privilege is introduced.
		if _, err = tx.Exec(f.ctx, "GRANT ALL PRIVILEGES ON TABLE pantry.leftover_operations TO household_owner"); err != nil {
			t.Fatal(err)
		}
		var ready, catalogEqual bool
		if err = tx.QueryRow(f.ctx, `SELECT pantry.prepared_capture_ready(),
 encode(sha256(convert_to(pantry.prepared_capture_catalog()::text,'UTF8')),'hex')=$1`, before).Scan(&ready, &catalogEqual); err != nil || !ready || !catalogEqual {
			t.Fatal("identical owner grant altered strict contract or other catalog dimensions", ready, catalogEqual, err)
		}
	})
	assertRestored(t)
	for _, test := range []struct{ name, sql string }{
		{"additional App maintain privilege", "GRANT MAINTAIN ON TABLE pantry.leftover_operations TO household_app"},
		{"additional public read privilege", "GRANT SELECT ON TABLE pantry.leftover_operations TO PUBLIC"},
		{"additional column ACL", "GRANT SELECT(id) ON TABLE pantry.leftover_operations TO household_app"},
		{"explicit grant option", "GRANT SELECT ON TABLE pantry.leftover_operations TO household_app WITH GRANT OPTION"},
		{"explicit empty owner ACL", "REVOKE ALL PRIVILEGES ON TABLE pantry.leftover_operations FROM household_owner"},
		{"catalog body drift", `CREATE OR REPLACE FUNCTION pantry.prepared_capture_catalog() RETURNS jsonb
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT ''{}''::jsonb'`},
	} {
		t.Run(test.name+" remains fail closed", func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tx.Rollback(cleanup)
			}()
			if _, err = tx.Exec(f.ctx, test.sql); err != nil {
				t.Fatal("install bounded rollback-only ACL/body fault", err)
			}
			var ready, changed bool
			if err = tx.QueryRow(f.ctx, `SELECT pantry.prepared_capture_ready(),
 encode(sha256(convert_to(pantry.prepared_capture_catalog()::text,'UTF8')),'hex')<>$1`, before).Scan(&ready, &changed); err != nil || ready || !changed {
				t.Fatal("default equivalence accepted a distinct explicit privilege/body contract", ready, changed, err)
			}
			if test.name == "additional App maintain privilege" {
				var ordinaryPrivateRights bool
				if err = tx.QueryRow(f.ctx, `SELECT has_table_privilege('household_app',
 'pantry.leftover_operations','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')`).Scan(&ordinaryPrivateRights); err != nil || ordinaryPrivateRights {
					t.Fatal("MAINTAIN negative case must exercise the exact ACL fingerprint independently", ordinaryPrivateRights, err)
				}
			}
		})
		assertRestored(t)
	}
}
