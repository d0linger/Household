//go:build integration

package web

import (
	"errors"
	"strings"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestIngredientCatalogNormalizationAndHistory(t *testing.T) {
	f := newIntegrationFixture(t)
	m, _ := f.app.Store.Module("recipes")
	catalogEntity, _ := m.Entity("ingredient_catalog")
	aliasEntity, _ := m.Entity("ingredient_aliases")
	ingredientEntity, _ := m.Entity("ingredients")
	cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "  Re\u0301is   aus   Österreich  "})
	cid := store.String(cat["id"])
	if cat["name"] != "Réis aus Österreich" || cat["name_key"] != "réis aus österreich" {
		t.Fatal("authoritative NFC/space/lower identity", cat)
	}
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, catalogEntity, "", value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"name": "RÉIS AUS ÖSTERREICH"}, 0)); err == nil {
		t.Fatal("canonical catalogue duplicate accepted")
	}
	alias, _ := auditCreate(t, f, "recipes", "ingredient_aliases", store.Record{"catalog_id": cid, "name": "  Ko\u0308rner   Reis "})
	if alias["name"] != "Körner Reis" || alias["name_key"] != "körner reis" {
		t.Fatal("canonical alias", alias)
	}
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, aliasEntity, "", value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"catalog_id": cid, "name": "KÖRNER REIS"}, 0)); err == nil {
		t.Fatal("normalized alias duplicate accepted")
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO recipes.ingredient_aliases(household_id,catalog_id,name,created_by)
 SELECT $1,$2,'Alias '||n,$3 FROM generate_series(2,50) n`, f.identity.HouseholdID, cid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO recipes.ingredient_aliases(household_id,catalog_id,name,created_by) VALUES($1,$2,'Overflow',$3)", f.identity.HouseholdID, cid, f.identity.UserID); err == nil {
		t.Fatal("direct SQL alias capacity bypass")
	}
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, aliasEntity, "", value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"catalog_id": cid, "name": "Overflow"}, 0)); err == nil {
		t.Fatal("application alias capacity bypass")
	}
	r, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Catalogue identity", "base_servings": "2"})
	ingredient, _ := auditCreate(t, f, "recipes", "ingredients", store.Record{"recipe_id": r["id"], "name": "Recipe spelling", "catalog_id": cid, "quantity": "100", "unit_code": "g"})
	if ingredient["catalog_label"] != "Réis aus Österreich" {
		t.Fatal("selection did not capture catalogue label", ingredient)
	}
	var version, snapshot string
	if err := f.pool.QueryRow(f.ctx, "SELECT id,snapshot::text FROM recipes.versions WHERE recipe_id=$1 ORDER BY revision DESC LIMIT 1", r["id"]).Scan(&version, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, catalogEntity, cid, value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"name": "Neue Katalogbezeichnung"}, store.Int(cat["version"]))); err != nil {
		t.Fatal(err)
	}
	current, err := f.app.Store.Get(f.ctx, f.identity, m, ingredientEntity, store.String(ingredient["id"]))
	if err != nil || current["catalog_label"] != "Réis aus Österreich" {
		t.Fatal("catalogue rename rewrote an existing selection", current, err)
	}
	bundle, err := f.app.Store.Recipe(f.ctx, f.identity, store.String(r["id"]))
	if err != nil {
		t.Fatal(err)
	}
	legacy := store.RecipeInput{Generation: f.app.Store.States()["recipes"].Generation, Fingerprint: bundle.Fingerprint,
		Data:        recipeData(t, store.Record{"title": "Edited by an older client", "base_servings": "2"}),
		Ingredients: []store.RecipeRow{{ID: store.String(ingredient["id"]), Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Recipe spelling", "quantity": "100", "unit_code": "g"})}},
	}
	if _, err = f.app.Store.SaveRecipe(f.ctx, f.identity, store.String(r["id"]), value.UUID(), value.UUID(), legacy); err != nil {
		t.Fatal("legacy workspace edit", err)
	}
	current, err = f.app.Store.Get(f.ctx, f.identity, m, ingredientEntity, store.String(ingredient["id"]))
	if err != nil || current["catalog_id"] != cid || current["catalog_label"] != "Réis aus Österreich" {
		t.Fatal("older workspace client silently removed an explicit catalogue selection", current, err)
	}
	if err = f.app.Store.Delete(f.ctx, f.identity, m, catalogEntity, cid, 2, f.app.Store.States()["recipes"].Generation, value.UUID()); err == nil {
		t.Fatal("catalogue deletion silently reassigned a live recipe")
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, ingredientEntity, store.String(ingredient["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"catalog_id": nil}, store.Int(current["version"]))); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.Delete(f.ctx, f.identity, m, catalogEntity, cid, 2, f.app.Store.States()["recipes"].Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var preserved string
	if err = f.pool.QueryRow(f.ctx, "SELECT snapshot::text FROM recipes.versions WHERE id=$1", version).Scan(&preserved); err != nil || preserved != snapshot || !strings.Contains(preserved, cid) {
		t.Fatal("old recipe snapshot identity or bytes changed", err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "REVOKE UPDATE ON recipes.ingredient_aliases FROM household_app"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.owner.Exec(f.ctx, "GRANT UPDATE ON recipes.ingredient_aliases TO household_app")
	})
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("readiness ignored one missing required privilege")
	}
}

func TestIngredientBindingReplayTombstoneAndLifecycle(t *testing.T) {
	f := newIntegrationFixture(t)
	cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Rice"})
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Pantry rice", "unit_code": "kg"})
	cid, pid := store.String(cat["id"]), store.String(product["id"])
	in := store.IngredientBindingInput{Generation: f.app.Store.States()["recipes"].Generation, PantryGeneration: f.app.Store.States()["pantry"].Generation, CatalogVersion: 1, ProductID: pid, ProductVersion: 1}
	key := value.UUID()
	first, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, key, value.UUID(), in)
	if err != nil || first["product_id"] != pid || store.Int(first["version"]) != 1 {
		t.Fatal("explicit binding", first, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.products SET name='Renamed rice' WHERE id=$1", pid); err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, key, value.UUID(), in)
	if err != nil || replay["id"] != first["id"] || store.Int(replay["version"]) != 1 {
		t.Fatal("binding replay reinterpreted changed product", replay, err)
	}
	changed := in
	changed.ProductVersion = 2
	if _, err = f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, key, value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("request key accepted changed binding body", err)
	}
	in.BindingID, in.Version, in.ProductID = store.String(first["id"]), 1, ""
	cleared, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in)
	if err != nil || cleared["id"] != first["id"] || store.Int(cleared["version"]) != 2 || cleared["product_id"] != nil {
		t.Fatal("clear did not retain versioned identity", cleared, err)
	}
	in.ProductID, in.ProductVersion = pid, 2
	if _, err = f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("pre-clear version became valid again", err)
	}
	in.Version = 2
	rebound, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in)
	if err != nil || rebound["id"] != first["id"] || store.Int(rebound["version"]) != 3 {
		t.Fatal("explicit rebind", rebound, err)
	}
	pm, _ := f.app.Store.Module("pantry")
	pe, _ := pm.Entity("products")
	if err = f.app.Store.Delete(f.ctx, f.identity, pm, pe, pid, 2, in.PantryGeneration, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var version int64
	var target *string
	if err = f.pool.QueryRow(f.ctx, "SELECT version,product_id::text FROM core.ingredient_product_bindings WHERE id=$1", first["id"]).Scan(&version, &target); err != nil || version != 4 || target != nil {
		t.Fatal("product deletion did not tombstone binding", version, target, err)
	}
	newProduct, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "New rice identity", "unit_code": "g"})
	in.Version, in.ProductID, in.ProductVersion = 4, store.String(newProduct["id"]), 1
	bound, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in)
	if err != nil || store.Int(bound["version"]) != 5 {
		t.Fatal(bound, err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disabled target remained bindable", err)
	}
	in.Version, in.ProductID = 5, ""
	unbound, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in)
	if err != nil || unbound["product_id"] != nil || unbound["assigned"] != false || store.Int(unbound["version"]) != 6 {
		t.Fatal("disabled target could not be safely unbound", unbound, err)
	}
	in.PantryGeneration++
	if _, err = f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale target generation accepted", err)
	}
}

func TestIngredientBindingConcurrentRebindSurvivesProductDelete(t *testing.T) {
	f := newIntegrationFixture(t)
	cat, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Concurrent catalogue"})
	oldProduct, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Product being deleted", "unit_code": "kg"})
	cid, oldID := store.String(cat["id"]), store.String(oldProduct["id"])
	in := store.IngredientBindingInput{Generation: f.app.Store.States()["recipes"].Generation, PantryGeneration: f.app.Store.States()["pantry"].Generation,
		CatalogVersion: 1, ProductID: oldID, ProductVersion: 1}
	binding, err := f.app.Store.SetIngredientBinding(f.ctx, f.identity, cid, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	held, err := f.app.Store.BeginMutation(f.ctx, f.identity, "recipes", in.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(f.ctx) }()
	var locked string
	if err = held.QueryRow(f.ctx, "SELECT id FROM recipes.ingredient_catalog WHERE household_id=$1 AND id=$2 FOR UPDATE", f.identity.HouseholdID, cid).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if err = held.QueryRow(f.ctx, "SELECT id FROM core.ingredient_product_bindings WHERE id=$1 FOR UPDATE", binding["id"]).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	var holder int32
	if err = held.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&holder); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	pm, _ := f.app.Store.Module("pantry")
	pe, _ := pm.Entity("products")
	go func() {
		done <- f.app.Store.Delete(f.ctx, f.identity, pm, pe, oldID, 1, in.PantryGeneration, value.UUID())
	}()
	// The deletion statement has acquired its snapshot before the replacement
	// product exists. Its cleanup must already be waiting on our binding row.
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE $1::int=ANY(pg_blocking_pids(a.pid)) AND a.wait_event_type='Lock')", holder).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("product deletion never waited on the controlled binding lock")
	}
	replacement, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Created after deletion snapshot", "unit_code": "g"})
	if _, err = held.Exec(f.ctx, "UPDATE core.ingredient_product_bindings SET product_id=$2,target_generation=$3,changed_by=$4 WHERE id=$1", binding["id"], replacement["id"], in.PantryGeneration, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = held.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("product deletion did not finish after rebind committed")
	}
	var target string
	var version int64
	if err = f.pool.QueryRow(f.ctx, "SELECT coalesce(product_id::text,''),version FROM core.ingredient_product_bindings WHERE id=$1", binding["id"]).Scan(&target, &version); err != nil || target != replacement["id"] || version != 2 {
		t.Fatal("deleting the old product erased a concurrent new binding", target, version, err)
	}
}
