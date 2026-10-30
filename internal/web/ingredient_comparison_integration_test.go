//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func ingredientBind(t *testing.T, f *integrationFixture, catalog, product store.Record) store.Record {
	t.Helper()
	result, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, store.String(catalog["id"]), value.UUID(), value.UUID(), store.IngredientBindingInput{
		Generation: f.app.Store.States()["recipes"].Generation, PantryGeneration: f.app.Store.States()["pantry"].Generation,
		CatalogVersion: store.Int(catalog["version"]), ProductID: store.String(product["id"]), ProductVersion: store.Int(product["version"]),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIngredientComparisonAggregatesBeforeSubtractAndHistoricalSource(t *testing.T) {
	f := newIntegrationFixture(t)
	p, lot := pantryFixture(t, f, "comparison")
	c1, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Rice"})
	c2, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Wholegrain rice"})
	ingredientBind(t, f, c1, p)
	ingredientBind(t, f, c2, p)
	r, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Combined demand", "base_servings": "2"})
	rid := store.String(r["id"])
	first, _ := auditCreate(t, f, "recipes", "ingredients", store.Record{"recipe_id": rid, "name": "First", "catalog_id": c1["id"], "quantity": "1000", "unit_code": "g"})
	auditCreate(t, f, "recipes", "ingredients", store.Record{"recipe_id": rid, "name": "Second", "catalog_id": c2["id"], "quantity": "1", "unit_code": "kg"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "0.97"})
	if _, err := f.pool.Exec(f.ctx, `WITH lots AS (
 INSERT INTO pantry.lots(household_id,product_id,location_id,received_on,best_before_on,created_by)
 SELECT $1,$2,$3,'2026-01-01','2026-10-25'::date+n,$4 FROM generate_series(1,53) n RETURNING id,household_id)
 INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) SELECT household_id,id,0.01,'receive',$4 FROM lots`,
		f.identity.HouseholdID, p["id"], lot["location_id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	archived, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Excluded archived location"})
	extra, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": p["id"], "location_id": archived["id"], "received_on": "2026-01-01", "notes": "Excluded lot"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": extra["id"], "reason": "receive", "quantity_delta": "100"})
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.locations SET archived_at=now() WHERE id=$1", archived["id"]); err != nil {
		t.Fatal(err)
	}
	out, err := f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, "", "2")
	if err != nil || len(out.Products) != 1 || out.Outcome != "shortage" {
		t.Fatal("combined comparison", out, err)
	}
	g := out.Products[0]
	if g.Demand.Rational != "2" || g.Stock.Rational != "3/2" || g.Shortage == nil || g.Shortage.Rational != "1/2" || g.LotCount != 54 || len(g.LotSample) != 3 {
		t.Fatal("demand duplicated stock or used bounded lot sample as total", g)
	}
	if g.LotSample[0]["priority_kind"] != "best_before" {
		t.Fatal("FEFO sample ignored known dates", g.LotSample)
	}
	vid := out.SourceVersionID
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "0.5"})
	if _, err = f.pool.Exec(f.ctx, "UPDATE recipes.ingredients SET quantity=2000,version=version+1 WHERE id=$1", first["id"]); err != nil {
		t.Fatal(err)
	}
	old, err := f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, vid, "2")
	if err != nil || !old.Historical || old.Products[0].Demand.Rational != "2" || old.Products[0].Stock.Rational != "2" {
		t.Fatal("historical source was reinterpreted", old, err)
	}
	current, err := f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, "", "2")
	if err != nil || current.Products[0].Demand.Rational != "3" || current.SourceVersionID == vid {
		t.Fatal("current source did not advance", current, err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.RecipePantryComparison(f.ctx, foreign, rid, vid, "2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign source disclosed", err)
	}
	if _, err = f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, value.UUID(), "2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unknown source silently fell back to current", err)
	}
	for _, portions := range []string{"0", "NaN", "-1", "1.0000001"} {
		if _, err = f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, vid, portions); err == nil {
			t.Fatal("invalid portions accepted", portions)
		}
	}
	for _, path := range []string{"/api/v1/recipes/" + rid + "/pantry-comparison?servings=2&version=" + vid, "/m/recipes/recipes/" + rid + "/pantry", "/m/recipes/catalog/" + store.String(c1["id"])} {
		response, raw, requestErr := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if requestErr != nil || response.StatusCode != 200 {
			t.Fatal("real comparison API/page", path, response, string(raw), requestErr)
		}
	}
}

func TestIngredientComparisonLifecycleReasons(t *testing.T) {
	f := newIntegrationFixture(t)
	cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Lifecycle rice"})
	p, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Lifecycle target", "unit_code": "kg"})
	ingredientBind(t, f, cat, p)
	r, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Lifecycle source", "base_servings": "1"})
	ing, _ := auditCreate(t, f, "recipes", "ingredients", store.Record{"recipe_id": r["id"], "name": "Rice", "catalog_id": cat["id"], "quantity": "1", "unit_code": "kg"})
	check := func(reason string) {
		t.Helper()
		out, err := f.app.Store.RecipePantryComparison(f.ctx, f.identity, store.String(r["id"]), "", "1")
		if err != nil || out.Outcome != "incomplete" || len(out.Lines) != 1 || out.Lines[0].Reason != reason || len(out.Products) != 0 {
			t.Fatal("lifecycle state", reason, out, err)
		}
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.products SET archived_at=now() WHERE id=$1", p["id"]); err != nil {
		t.Fatal(err)
	}
	check("product_archived")
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.products SET archived_at=NULL WHERE id=$1", p["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE recipes.ingredient_catalog SET archived=true WHERE id=$1", cat["id"]); err != nil {
		t.Fatal(err)
	}
	check("catalog_archived")
	if _, err := f.pool.Exec(f.ctx, "UPDATE recipes.ingredient_catalog SET archived=false WHERE id=$1", cat["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET data_generation=data_generation+1 WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	check("binding_stale")
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET data_generation=data_generation-1 WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	// Simulate an operator removing the endpoint outside generic CRUD. Reads
	// must not trust the lingering cross-module UUID without a live endpoint.
	if _, err := f.owner.Exec(f.ctx, "DELETE FROM pantry.products WHERE id=$1", p["id"]); err != nil {
		t.Fatal(err)
	}
	check("product_missing")
	var vid string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM recipes.versions WHERE recipe_id=$1 ORDER BY revision DESC LIMIT 1", r["id"]).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE recipes.ingredients SET catalog_id=NULL WHERE id=$1", ing["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "DELETE FROM recipes.ingredient_catalog WHERE id=$1", cat["id"]); err != nil {
		t.Fatal(err)
	}
	old, err := f.app.Store.RecipePantryComparison(f.ctx, f.identity, store.String(r["id"]), vid, "1")
	if err != nil || old.Lines[0].Reason != "catalog_missing" || old.Lines[0].CatalogLabel != "Lifecycle rice" {
		t.Fatal("historical unavailable catalogue reinterpreted", old, err)
	}
}

func TestIngredientPopulatedUpgradePreservesUnassignedHistory(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		switch schema {
		case "core":
			migratePriorAuditSchema(t, f, schema, 14)
		case "recipes":
			migratePriorAuditSchema(t, f, schema, 3)
		default:
			migratePriorAuditSchema(t, f, schema)
		}
	}
	hid, uid, rid := value.UUID(), value.UUID(), value.UUID()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior ingredient release')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'old-ingredient','Prior','not-a-login')", []any{uid}},
		{"INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES($1,$2,'Untouched recipe',2.500001,$3)", []any{rid, hid, uid}},
		{"INSERT INTO recipes.ingredients(household_id,recipe_id,name,quantity,unit_code,created_by) VALUES($1,$2,'Old rice',9007199254.740993,'g',$3)", []any{hid, rid, uid}},
	} {
		if _, err := f.owner.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	var before, after string
	if err := f.owner.QueryRow(f.ctx, "SELECT md5(string_agg(snapshot::text,',' ORDER BY revision)) FROM recipes.versions WHERE recipe_id=$1", rid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT md5(string_agg(snapshot::text,',' ORDER BY revision)) FROM recipes.versions WHERE recipe_id=$1", rid).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade rewrote immutable snapshots", before, after, err)
	}
	var unchanged bool
	if err := f.pool.QueryRow(f.ctx, `SELECT bool_and(catalog_id IS NULL AND catalog_label='' AND quantity=9007199254.740993 AND version=1)
 AND NOT EXISTS(SELECT 1 FROM recipes.ingredient_catalog) AND NOT EXISTS(SELECT 1 FROM core.ingredient_product_bindings)
 FROM recipes.ingredients WHERE recipe_id=$1`, rid).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("upgrade invented identity or changed legacy row", unchanged, err)
	}
}

func TestIngredientComparisonUnknownAndDisabledNeverMeanZeroShortage(t *testing.T) {
	f := newIntegrationFixture(t)
	p, _ := pantryFixture(t, f, "unknown")
	cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Explicit rice"})
	ingredientBind(t, f, cat, p)
	r, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Incomplete comparison", "base_servings": "3"})
	rid := store.String(r["id"])
	for _, row := range []store.Record{
		{"name": "Small exact", "catalog_id": cat["id"], "quantity": "0.000001", "unit_code": "g"},
		{"name": "Unknown amount", "catalog_id": cat["id"], "quantity_note": "to taste"},
		{"name": "Wrong dimension", "catalog_id": cat["id"], "quantity": "1", "unit_code": "ml"},
		{"name": "Same spelling is not an assignment", "quantity": "1", "unit_code": "kg"},
	} {
		row["recipe_id"] = rid
		auditCreate(t, f, "recipes", "ingredients", row)
	}
	out, err := f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, "", "0.000001")
	if err != nil || out.Outcome != "incomplete" || len(out.Products) != 1 || out.Products[0].Shortage != nil || out.Products[0].Complete {
		t.Fatal("unknown demand represented as complete shortage", out, err)
	}
	reasons := map[string]bool{}
	for _, row := range out.Lines {
		reasons[row.Reason] = true
		if row.Name == "Small exact" && (row.Demand == nil || !row.Demand.BelowPrecision || row.Demand.Rational == "0") {
			t.Fatal("sub-micro demand disappeared", row)
		}
	}
	for _, reason := range []string{"quantity_unknown", "unit_incompatible", "catalog_unassigned"} {
		if !reasons[reason] {
			t.Fatal("missing explicit unknown state", reason, out.Lines)
		}
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	out, err = f.app.Store.RecipePantryComparison(f.ctx, f.identity, rid, "", "2")
	if err != nil || out.PantryEnabled || len(out.Products) != 0 || out.Outcome != "incomplete" {
		t.Fatal("disabled target became sufficient or disclosed stock", out, err)
	}
	for _, row := range out.Lines {
		if row.ProductID != "" || row.CatalogID != "" && row.Reason != "pantry_disabled" {
			t.Fatal("disabled target identity or wrong reason", row)
		}
	}
	view, err := f.app.Store.IngredientCatalogView(f.ctx, f.identity, store.String(cat["id"]))
	encoded, _ := json.Marshal(view)
	if err != nil || strings.Contains(string(encoded), store.String(p["id"])) || strings.Contains(string(encoded), store.String(p["name"])) {
		t.Fatal("disabled product leaked through catalogue", string(encoded), err)
	}
}

func TestIngredientCatalogHTTPBoundariesAliasSearchAndConcurrentWriters(t *testing.T) {
	f := newIntegrationFixture(t)
	cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Canonical Rice"})
	auditCreate(t, f, "recipes", "ingredient_aliases", store.Record{"catalog_id": cat["id"], "name": "Körner  Reis"})
	p, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Explicit target", "unit_code": "kg"})
	cid := store.String(cat["id"])
	path := "/api/v1/ingredient-catalog/" + cid
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, tc := range []struct {
		method, path, token, csrf, body string
		want                            int
	}{
		{http.MethodGet, path, "", "", "", 401},
		{http.MethodGet, path, f.token, "", "", 200},
		{http.MethodGet, "/api/v1/ingredient-catalog/" + value.UUID(), f.token, "", "", 404},
		{http.MethodPost, path + "/binding", f.token, "", "{}", 403},
		{http.MethodPost, path + "/binding", f.token, csrf, `{"administrator":true}`, 422},
		{http.MethodPost, path + "/binding", f.token, csrf, `{"generation":"1"}`, 422},
	} {
		response, raw, err := f.request(tc.method, tc.path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
		if err != nil || response.StatusCode != tc.want {
			t.Fatal(tc, response, string(raw), err)
		}
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/core/reference-options/recipes/ingredient_catalog?q="+url.QueryEscape(" KO\u0308RNER REIS "), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), cid) || !strings.Contains(string(raw), "Canonical Rice") {
		t.Fatal("alias search did not return explicit canonical option", response, string(raw), err)
	}
	shoppingTestMember(t, f, "ingredient-reader", "reader")
	reader, _, err := f.app.Auth.Login(f.ctx, "ingredient-reader", integrationPassword, "", "127.0.0.1", "ingredient-reader")
	if err != nil {
		t.Fatal(err)
	}
	in := store.IngredientBindingInput{Generation: f.app.Store.States()["recipes"].Generation, PantryGeneration: f.app.Store.States()["pantry"].Generation,
		CatalogVersion: 1, ProductID: store.String(p["id"]), ProductVersion: 1}
	encoded, _ := json.Marshal(in)
	response, raw, err = f.request(http.MethodPost, path+"/binding", reader, f.app.Auth.CSRF(reader, "session"), "application/json", strings.NewReader(string(encoded)), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("reader changed assignment", response, string(raw), err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.IngredientCatalogView(f.ctx, foreign, cid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign catalogue disclosed", err)
	}
	done := make(chan error, 8)
	start := make(chan struct{})
	for range 8 {
		go func() {
			<-start
			_, e := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in)
			done <- e
		}()
	}
	close(start)
	success := 0
	for range 8 {
		e := <-done
		if e == nil {
			success++
		} else if !errors.Is(e, store.ErrConflict) {
			t.Fatal("unexpected concurrent result", e)
		}
	}
	if success != 1 {
		t.Fatal("expected versions did not serialize first assignment", success)
	}
	var count, audits int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.ingredient_product_bindings WHERE catalog_id=$1", cid).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate binding", count, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE resource_type='ingredient_product_bindings' AND action='CREATE'").Scan(&audits); err != nil || audits != 1 {
		t.Fatal("binding audit was not atomic", audits, err)
	}
}

func TestIngredientBindingsPreserveIndependentTargetsOnRealModulePurge(t *testing.T) {
	for _, mid := range []string{"pantry", "shopping", "recipes"} {
		t.Run(mid, func(t *testing.T) {
			f := newIntegrationFixture(t)
			cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Purge identity"})
			p, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Purge target", "unit_code": "kg"})
			bound := ingredientBind(t, f, cat, p)
			shoppingProduct := value.UUID()
			if _, err := f.owner.Exec(f.ctx, `INSERT INTO shopping.products
 (id,household_id,name,pack_quantity,pack_unit,created_by)
 VALUES($1,$2,'Independent shopping target',1,'kg',$3)`, shoppingProduct,
				f.identity.HouseholdID, f.identity.UserID); err != nil {
				t.Fatal("seed independent Shopping target", err)
			}
			shoppingGeneration := f.app.Store.States()["shopping"].Generation
			if _, err := f.app.Store.SaveRecipeShoppingBinding(f.ctx, f.identity,
				store.String(cat["id"]), value.UUID(), value.UUID(), store.RecipePriceBindingInput{
					Generation:         f.app.Store.States()["recipes"].Generation,
					ShoppingGeneration: shoppingGeneration,
					CatalogVersion:     store.Int(cat["version"]),
					BindingID:          store.String(bound["id"]), Version: store.Int(bound["version"]),
					ProductID: shoppingProduct, ProductVersion: 1,
				}); err != nil {
				t.Fatal("bind independent Shopping target", err)
			}
			state := f.app.Store.States()[mid]
			if err := f.app.Store.SetModule(f.ctx, f.identity, mid, false, state.Version, value.UUID()); err != nil {
				t.Fatal(err)
			}
			backup, purge := value.UUID(), value.UUID()
			if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,$3,$4,$5,$6)", purge, f.identity.HouseholdID, mid, state.Generation, f.identity.UserID, backup); err != nil {
				t.Fatal(err)
			}
			if err := maintenance.Purge(f.ctx, f.owner, purge); err != nil {
				t.Fatal(err)
			}
			var left int
			var pantryTarget, shoppingTarget string
			var pantryGeneration, retainedShoppingGeneration int64
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*),coalesce(max(product_id::text),''),
 coalesce(max(shopping_product_id::text),''),coalesce(max(target_generation),0),
 coalesce(max(shopping_generation),0) FROM core.ingredient_product_bindings
 WHERE household_id=$1`, f.identity.HouseholdID).Scan(&left, &pantryTarget,
				&shoppingTarget, &pantryGeneration, &retainedShoppingGeneration); err != nil {
				t.Fatal("read bindings after real purge", err)
			}
			switch mid {
			case "pantry":
				if left != 1 || pantryTarget != "" || pantryGeneration != 0 ||
					shoppingTarget != shoppingProduct || retainedShoppingGeneration != shoppingGeneration {
					t.Fatalf("Pantry purge changed Shopping target: %d/%q/%d/%q/%d", left,
						pantryTarget, pantryGeneration, shoppingTarget, retainedShoppingGeneration)
				}
			case "shopping":
				if left != 1 || pantryTarget != store.String(p["id"]) ||
					pantryGeneration != f.app.Store.States()["pantry"].Generation ||
					shoppingTarget != "" || retainedShoppingGeneration != 0 {
					t.Fatalf("Shopping purge changed Pantry target: %d/%q/%d/%q/%d", left,
						pantryTarget, pantryGeneration, shoppingTarget, retainedShoppingGeneration)
				}
			case "recipes":
				if left != 0 {
					t.Fatalf("Recipes purge retained source binding: %d", left)
				}
			}
			var generation int64
			if err := f.pool.QueryRow(f.ctx, "SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id=$2", f.identity.HouseholdID, mid).Scan(&generation); err != nil || generation != state.Generation+1 {
				t.Fatal("purge generation", generation, err)
			}
		})
	}
}
