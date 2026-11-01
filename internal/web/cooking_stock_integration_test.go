//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func TestCookingStockCompletionGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	insert := `INSERT INTO core.cooking_stock_postings(id,household_id,recipes_generation,cooking_record_id,source_version_id,pantry_generation,created_by,request_key,request_hash,decision_count,movement_count,mode)
 VALUES($1,$2,1,$3,$4,1,$5,$6,decode(repeat('00',32),'hex'),0,$7,$8)`
	for _, mode := range []string{"stock", "no_stock"} {
		t.Run(mode+" proof alone", func(t *testing.T) {
			tx, err := audit.Begin(f.ctx, f.pool)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(f.ctx) }()
			count := 0
			if mode == "stock" {
				count = 1
			}
			if _, err = tx.Exec(f.ctx, insert, value.UUID(), f.identity.HouseholdID, value.UUID(), value.UUID(), f.identity.UserID, value.UUID(), count, mode); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(f.ctx); err == nil || !strings.Contains(err.Error(), "incomplete cooking movement bundle") {
				t.Fatal("proof-only transaction committed", err)
			}
		})
	}
	posting, record, version := value.UUID(), value.UUID(), value.UUID()
	tx, err := audit.Begin(f.ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, insert, posting, f.identity.HouseholdID, record, version, f.identity.UserID, value.UUID(), 0, "no_stock"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO pantry.cooking_stock_receipts(id,household_id,snapshot) VALUES($1,$2,jsonb_build_object('source',jsonb_build_object('cooking_record_id',$3::text,'source_version_id',$4::text),'decisions','[]'::jsonb))`, posting, f.identity.HouseholdID, record, version); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("complete no-stock bundle rejected", err)
	}
	if _, err = f.owner.Exec(f.ctx, "TRUNCATE pantry.cooking_stock_receipts CASCADE"); err != nil {
		t.Fatal(err)
	}
	var retained bool
	if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.cooking_stock_postings WHERE id=$1)", posting).Scan(&retained); err != nil || !retained {
		t.Fatal("purge lost durable proof", err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.cooking_stock_postings DISABLE TRIGGER cooking_posting_complete"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("readiness accepted disabled completion guard")
	}
}

func TestCookingStockDatabaseFailureRollsBackWholeBundle(t *testing.T) {
	f := newIntegrationFixture(t)
	_, record, in := cookingStockFixture(t, f)
	first, err := f.app.Store.PantryLotState(f.ctx, f.identity, in.Lots[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": first["product_id"], "location_id": first["location_id"], "received_on": "2026-10-25", "notes": "Second controlled rollback lot"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": second["id"], "reason": "receive", "quantity_delta": "3"})
	secondState, err := f.app.Store.PantryLotState(f.ctx, f.identity, store.String(second["id"]))
	if err != nil {
		t.Fatal(err)
	}
	in.Decisions[1].Allocations[0].LotID = store.String(second["id"])
	in.Lots = append(in.Lots, store.CookingStockLotReview{ID: store.String(second["id"]), Version: store.Int(secondState["version"]), LocationVersion: store.Int(secondState["location_version"]), MovementCount: store.Int(secondState["movement_count"]), Balance: store.String(secondState["stock"])})
	ids := []string{in.Lots[0].ID, in.Lots[1].ID}
	slices.Sort(ids)
	// Let the first lot really write, then reject the second lot in PostgreSQL.
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION pantry.test_reject_second_cooking_lot() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$ BEGIN
 IF NEW.reason='consume' AND NEW.lot_id=TG_ARGV[0]::uuid THEN RAISE EXCEPTION 'synthetic second-lot failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "CREATE TRIGGER test_cooking_lot_failure BEFORE INSERT ON pantry.stock_movements FOR EACH ROW EXECUTE FUNCTION pantry.test_reject_second_cooking_lot('"+ids[1]+"')"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in); err == nil || !strings.Contains(err.Error(), "synthetic second-lot failure") {
		t.Fatal("controlled failure absent", err)
	}
	var dirty bool
	if err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.cooking_stock_postings) OR EXISTS(SELECT 1 FROM pantry.cooking_stock_receipts)
 OR EXISTS(SELECT 1 FROM pantry.cooking_stock_movements) OR EXISTS(SELECT 1 FROM pantry.stock_operations)
 OR EXISTS(SELECT 1 FROM pantry.stock_movements WHERE reason='consume') OR EXISTS(SELECT 1 FROM pantry.lot_balances WHERE quantity<>3 OR movement_count<>1)`).Scan(&dirty); err != nil || dirty {
		t.Fatal("partial cooking effects escaped rollback", dirty, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DROP TRIGGER test_cooking_lot_failure ON pantry.stock_movements"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("clean retry after rollback", err)
	}
}

func TestCookingStockPopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		v := uint(database.ExpectedVersion(schema))
		if schema == "chores" {
			v = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		if schema == "recipes" {
			v = 4 // Recipes5 belongs to the later Core27 expansion.
		}
		if schema == "core" {
			v = 15
		}
		if schema == "fuel" {
			v = 2 // Fuel5 requires Core26; preserve this Core15 predecessor.
		}
		if schema == "shopping" {
			v = 4 // MP17 Shopping receipts require Core21.
		}
		if schema == "pantry" {
			v = 3
		}
		if schema == "mealplan" {
			v = 2 // This fixture predates the Core reservation contract.
		}
		if schema == "expenses" {
			v = 3 // Expenses4 requires the later Core25 contract.
		}
		migratePriorAuditSchema(t, f, schema, v)
	}
	hh, user, product, location, lot, recipe, session, record := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	tx, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior cooking-stock release')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-cooking-stock','Prior user','fixture-no-login')", []any{user}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{hh, user}},
		{"INSERT INTO pantry.products(id,household_id,name,unit_code,created_by) VALUES($1,$2,'Prior exact stock','kg',$3)", []any{product, hh, user}},
		{"INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,'Prior shelf',$3)", []any{location, hh, user}},
		{"INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,notes,created_by) VALUES($1,$2,$3,$4,'2026-10-25','Prior lot',$5)", []any{lot, hh, product, location, user}},
		{"INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,9007199254.740993,'receive',$3)", []any{hh, lot, user}},
		{"INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES($1,$2,'Prior recipe',2,$3)", []any{recipe, hh, user}},
		{"INSERT INTO recipes.ingredients(household_id,recipe_id,name,quantity,unit_code,created_by) VALUES($1,$2,'Prior ingredient',1000,'g',$3)", []any{hh, recipe, user}},
		{"INSERT INTO recipes.steps(household_id,recipe_id,instruction,created_by) VALUES($1,$2,'Prior preparation',$3)", []any{hh, recipe, user}},
	} {
		if _, err = tx.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "INSERT INTO recipes.cooking_sessions(id,household_id,user_id,source_version_id,servings) SELECT $1,$2,$3,id,4 FROM recipes.versions WHERE recipe_id=$4 AND revision=1", session, hh, user, recipe); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO recipes.cooking_records(id,household_id,session_id,user_id,source_version_id,actual_servings,cooked_on) SELECT $1,$2,$3,$4,id,1,'2026-10-25' FROM recipes.versions WHERE recipe_id=$5 AND revision=1", record, hh, session, user, recipe); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE recipes.cooking_sessions SET state='completed' WHERE id=$1", session); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = f.pool.QueryRow(f.ctx, "SELECT snapshot::text FROM recipes.versions WHERE recipe_id=$1 AND revision=1", recipe).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var stock, servings, after string
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lot).Scan(&stock); err != nil || stock != "9007199254.740993" {
		t.Fatal("upgrade changed exact stock", stock, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT actual_servings::text FROM recipes.cooking_records WHERE id=$1", record).Scan(&servings); err != nil || servings != "1.000000" {
		t.Fatal("upgrade changed completed portions", servings, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT snapshot::text FROM recipes.versions WHERE recipe_id=$1 AND revision=1", recipe).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade rewrote historical source", err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.cooking_stock_postings").Scan(&count); err != nil || count != 0 {
		t.Fatal("upgrade automatically consumed prior cooking", count, err)
	}
}

func cookingStockFixture(t *testing.T, f *integrationFixture) (string, string, store.CookingStockInput) {
	t.Helper()
	p, lot := pantryFixture(t, f, "cooking stock")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "3"})
	r, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Stock source", "base_servings": "2"})
	rid := store.String(r["id"])
	catalog, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Cooking stock identity"})
	ingredientBind(t, f, catalog, p)
	for _, name := range []string{"First ingredient", "Second ingredient"} {
		auditCreate(t, f, "recipes", "ingredients", store.Record{"recipe_id": rid, "name": name, "catalog_id": catalog["id"], "quantity": "1000", "unit_code": "g"})
	}
	auditCreate(t, f, "recipes", "steps", store.Record{"recipe_id": rid, "instruction": "Zutaten zubereiten"})
	s := cookingStart(t, f, rid)
	finished, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), store.CookingInput{Action: "finish", Generation: f.app.Store.States()["recipes"].Generation, Version: s.Version, ActualServings: "1", CookedOn: s.Today})
	if err != nil {
		t.Fatal(err)
	}
	record := store.String(finished["record_id"])
	preview, err := f.app.Store.CookingStockPreview(f.ctx, f.identity, record)
	if err != nil {
		t.Fatal("source preview", finished, err)
	}
	source := preview["source"].(map[string]any)
	state, err := f.app.Store.PantryLotState(f.ctx, f.identity, store.String(lot["id"]))
	if err != nil {
		t.Fatal(err)
	}
	in := store.CookingStockInput{Generation: f.app.Store.States()["recipes"].Generation, PantryGeneration: f.app.Store.States()["pantry"].Generation, SourceVersionID: store.String(source["source_version_id"]), Lots: []store.CookingStockLotReview{{ID: store.String(lot["id"]), Version: store.Int(state["version"]), LocationVersion: store.Int(state["location_version"]), MovementCount: store.Int(state["movement_count"]), Balance: store.String(state["stock"])}}}
	for _, raw := range source["ingredients"].([]any) {
		row := raw.(map[string]any)
		in.Decisions = append(in.Decisions, store.CookingStockDecision{IngredientID: store.String(row["id"]), Action: "consume", Selection: "manual", Reason: "Einmalige bestätigte Auswahl", ProductID: store.String(p["id"]), ProductVersion: store.Int(p["version"]), Allocations: []store.CookingStockAllocation{{LotID: store.String(lot["id"]), Quantity: "0.5"}}})
	}
	return rid, record, in
}

func TestCookingStockFreshReviewAndConcurrentActors(t *testing.T) {
	f := newIntegrationFixture(t)
	_, record, in := cookingStockFixture(t, f)
	lid := in.Lots[0].ID
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "receive", "quantity_delta": "0.1"})
	if _, err := f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale journal accepted", err)
	}
	l, err := f.app.Store.PantryLotState(f.ctx, f.identity, lid)
	if err != nil {
		t.Fatal(err)
	}
	in.Lots[0].Balance = store.String(l["stock"])
	in.Lots[0].MovementCount = store.Int(l["movement_count"])
	other := shoppingTestMember(t, f, "concurrent-cooking-editor", "editor")
	type answer struct {
		result store.Record
		err    error
	}
	answers := make(chan answer, 2)
	start := make(chan struct{})
	for _, actor := range []auth.Identity{f.identity, other} {
		go func(id auth.Identity) {
			<-start
			r, e := f.app.Store.PostCookingStock(f.ctx, id, record, value.UUID(), value.UUID(), in)
			answers <- answer{r, e}
		}(actor)
	}
	close(start)
	first, second := <-answers, <-answers
	if first.err != nil || second.err != nil || first.result["id"] != second.result["id"] {
		t.Fatal("once-only actor race", first, second)
	}
	var count int
	var stock string
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.stock_movements WHERE reason='consume'").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate aggregate movements", count, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lid).Scan(&stock); err != nil || stock != "2.100000" {
		t.Fatal(stock, err)
	}
}

func TestCookingStockCatalogTombstoneAndExplicitNoStock(t *testing.T) {
	f := newIntegrationFixture(t)
	_, record, in := cookingStockFixture(t, f)
	preview, err := f.app.Store.CookingStockPreview(f.ctx, f.identity, record)
	if err != nil {
		t.Fatal(err)
	}
	rows := preview["source"].(map[string]any)["ingredients"].([]any)
	for i := range in.Decisions {
		mapping := rows[i].(map[string]any)["mapping"].(map[string]any)
		d := &in.Decisions[i]
		d.Selection = "catalog"
		d.Reason = ""
		d.CatalogVersion = store.Int(mapping["catalog_version"])
		d.BindingID = store.String(mapping["binding_id"])
		d.BindingVersion = store.Int(mapping["binding_version"])
	}
	if _, err = f.app.Store.SetIngredientBinding(f.ctx, f.identity, store.String(rows[0].(map[string]any)["catalog_id"]), value.UUID(), value.UUID(), store.IngredientBindingInput{Generation: in.Generation, PantryGeneration: in.PantryGeneration, CatalogVersion: in.Decisions[0].CatalogVersion, BindingID: in.Decisions[0].BindingID, Version: in.Decisions[0].BindingVersion}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(in)
	response, raw, err := f.request(http.MethodPost, "/api/v1/cooking/history/"+record+"/stock", f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", strings.NewReader(string(body)), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 409 {
		t.Fatal("unbind must conflict without internal error", response, string(raw), err)
	}
	for i := range in.Decisions {
		in.Decisions[i] = store.CookingStockDecision{IngredientID: in.Decisions[i].IngredientID, Action: "skip", Reason: "Nicht aus dem Vorrat verwendet"}
	}
	in.Lots = nil
	if _, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("all-skip silently completed")
	}
	in.ConfirmNoStock = true
	result, err := f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in)
	if err != nil || result["mode"] != "no_stock" || len(result["movements"].([]any)) != 0 {
		t.Fatal("explicit all-skip", result, err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.stock_movements WHERE reason='consume'").Scan(&count); err != nil || count != 0 {
		t.Fatal("no-stock wrote journal", count, err)
	}
}

func TestCookingStockReceiptSurvivesRecipesPurge(t *testing.T) {
	f := newIntegrationFixture(t)
	_, record, in := cookingStockFixture(t, f)
	result, err := f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, purge := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'recipes',$3,$4,$5)", purge, f.identity.HouseholdID, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.app.Store.CookingStockReceipt(f.ctx, f.identity, store.String(result["id"]))
	if err != nil || receipt["source"].(map[string]any)["title"] != "Stock source" {
		t.Fatal("Pantry receipt depends on purged Recipes", receipt, err)
	}
	movement := receipt["movements"].([]any)[0].(map[string]any)
	undo := pantryStockInput(t, f, in.Lots[0].ID)
	undo.Action = "undo"
	undo.MovementID = store.String(movement["id"])
	if _, err = f.app.Store.MutatePantryStock(f.ctx, f.identity, in.Lots[0].ID, value.UUID(), value.UUID(), undo); err != nil {
		t.Fatal("source purge prevented journal correction", err)
	}
	var retained bool
	if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.cooking_stock_postings WHERE cooking_record_id=$1)", record).Scan(&retained); err != nil || !retained {
		t.Fatal("source purge removed permanent proof", err)
	}
}

func TestCookingStockAtomicHistoryReplayAndUndo(t *testing.T) {
	f := newIntegrationFixture(t)
	rid, record, in := cookingStockFixture(t, f)
	m, _ := f.app.Store.Module("recipes")
	e, _ := m.Entity("recipes")
	if err := f.app.Store.Delete(f.ctx, f.identity, m, e, rid, 1, in.Generation, value.UUID()); err != nil {
		t.Fatal("live recipe deletion", err)
	}
	preview, err := f.app.Store.CookingStockPreview(f.ctx, f.identity, record)
	if err != nil || preview["source"].(map[string]any)["actual_servings"] != "1.000000" {
		t.Fatal("completed source depends on live recipe", preview, err)
	}
	result, err := f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	movements := result["movements"].([]any)
	if len(movements) != 1 || movements[0].(map[string]any)["quantity"] != "1.000000" {
		t.Fatal("shared lot was not aggregated", result)
	}
	lid := in.Lots[0].ID
	lot, err := f.app.Store.PantryLotState(f.ctx, f.identity, lid)
	if err != nil || lot["stock"] != "2.000000" {
		t.Fatal(lot, err)
	}
	other := shoppingTestMember(t, f, "cooking-editor", "editor")
	replay, err := f.app.Store.PostCookingStock(f.ctx, other, record, value.UUID(), value.UUID(), in)
	if err != nil || replay["id"] != result["id"] {
		t.Fatal("actor-independent replay", replay, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	undo := pantryStockInput(t, f, lid)
	undo.Action = "undo"
	undo.MovementID = store.String(movements[0].(map[string]any)["id"])
	if _, err = f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), undo); err != nil {
		t.Fatal("cooking movement cannot be undone", err)
	}
	receipt, err := f.app.Store.CookingStockReceipt(f.ctx, f.identity, store.String(result["id"]))
	if err != nil || receipt["movements"].([]any)[0].(map[string]any)["reversed"] != true {
		t.Fatal("reversal state absent", receipt, err)
	}
	replay, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in)
	if err != nil || replay["id"] != result["id"] {
		t.Fatal("undo reopened source", err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	replay, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in)
	if err != nil || replay["details_available"] != false || replay["source"] != nil {
		t.Fatal("disabled target disclosed details", replay, err)
	}
	backup, purge := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'pantry',$3,$4,$5)", purge, f.identity.HouseholdID, in.PantryGeneration, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	replay, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in)
	if err != nil || replay["details_available"] != false || replay["id"] != result["id"] {
		t.Fatal("purge lost once-only replay", replay, err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", true, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	in.PantryGeneration = f.app.Store.States()["pantry"].Generation
	if _, err = f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("new target generation reopened completed source", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.stock_movements").Scan(&count); err != nil || count != 0 {
		t.Fatal("purge replay recreated stock", count, err)
	}
}

func TestCookingStockReviewRollbackAndHTTP(t *testing.T) {
	f := newIntegrationFixture(t)
	_, record, in := cookingStockFixture(t, f)
	bad := in
	bad.Decisions = append([]store.CookingStockDecision{}, in.Decisions...)
	bad.Decisions[1].IngredientID = value.UUID()
	if _, err := f.app.Store.PostCookingStock(f.ctx, f.identity, record, value.UUID(), value.UUID(), bad); err == nil {
		t.Fatal("foreign final ingredient accepted")
	}
	var dirty bool
	if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.cooking_stock_postings) OR EXISTS(SELECT 1 FROM pantry.stock_operations) OR EXISTS(SELECT 1 FROM pantry.stock_movements WHERE reason='consume')").Scan(&dirty); err != nil || dirty {
		t.Fatal("rejected review left partial effects", dirty, err)
	}
	body, _ := json.Marshal(in)
	path := "/api/v1/cooking/history/" + record + "/stock"
	shoppingTestMember(t, f, "cooking-stock-reader", "reader")
	reader, _, loginErr := f.app.Auth.Login(f.ctx, "cooking-stock-reader", integrationPassword, "", "127.0.0.1", "stock-reader")
	if loginErr != nil {
		t.Fatal(loginErr)
	}
	for _, tc := range []struct {
		name, method, token, csrf, body string
		want                            int
	}{
		{"anonymous", http.MethodGet, "", "", "", 401},
		{"csrf", http.MethodPost, f.token, "", string(body), 403},
		{"reader", http.MethodPost, reader, f.app.Auth.CSRF(reader, "session"), string(body), 403},
		{"unknown field", http.MethodPost, f.token, f.app.Auth.CSRF(f.token, "session"), `{"admin":true}`, 422},
		{"source", http.MethodGet, f.token, "", "", 200},
		{"post", http.MethodPost, f.token, f.app.Auth.CSRF(f.token, "session"), string(body), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, raw, err := f.request(tc.method, path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || res.StatusCode != tc.want {
				t.Fatal(res, string(raw), err)
			}
		})
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err := f.app.Store.CookingStockPreview(f.ctx, foreign, record); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign source disclosed", err)
	}
	for _, table := range []string{"core.cooking_stock_postings", "pantry.cooking_stock_receipts", "pantry.cooking_stock_movements"} {
		for _, query := range []string{"DELETE FROM " + table, "UPDATE " + table + " SET household_id=household_id"} {
			if _, err := f.pool.Exec(f.ctx, query); err == nil {
				t.Fatal("runtime mutated immutable stock proof", table)
			}
		}
	}
}
