//go:build integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func recipeData(t *testing.T, data store.Record) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func recipeInput(t *testing.T, f *integrationFixture) store.RecipeInput {
	t.Helper()
	return store.RecipeInput{Generation: f.app.Store.States()["recipes"].Generation, Data: recipeData(t, store.Record{"title": "Precise recipe", "base_servings": "2.500001", "prep_minutes": "5", "cook_minutes": "15"}),
		Ingredients: []store.RecipeRow{{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Precise ingredient", "quantity": "9007199254.740993", "unit_code": "g", "section_name": "Teig"})}, {Key: value.UUID(), Data: recipeData(t, store.Record{"name": "To taste", "quantity_note": "nach Geschmack", "unit_code": ""})}},
		Steps:       []store.RecipeRow{{Key: value.UUID(), Data: recipeData(t, store.Record{"instruction": "Mix everything", "timer_seconds": "0"})}, {Key: value.UUID(), Data: recipeData(t, store.Record{"instruction": "Wait", "timer_seconds": "90"})}}}
}
func recipeEdit(t *testing.T, f *integrationFixture, rid string) (store.RecipeInput, store.RecipeBundle) {
	t.Helper()
	bundle, err := f.app.Store.Recipe(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	input := store.RecipeInput{Generation: f.app.Store.States()["recipes"].Generation, Fingerprint: bundle.Fingerprint}
	m, _ := f.app.Store.Module("recipes")
	copyFields := func(name string, record store.Record) map[string]json.RawMessage {
		e, _ := m.Entity(name)
		data := store.Record{}
		for _, field := range e.Fields {
			if field.Name != "recipe_id" && field.Name != "position" {
				data[field.Name] = record[field.Name]
			}
		}
		return recipeData(t, data)
	}
	input.Data = copyFields("recipes", bundle.Recipe)
	for _, row := range bundle.Ingredients {
		input.Ingredients = append(input.Ingredients, store.RecipeRow{Key: value.UUID(), ID: store.String(row["id"]), Data: copyFields("ingredients", row)})
	}
	for _, row := range bundle.Steps {
		input.Steps = append(input.Steps, store.RecipeRow{Key: value.UUID(), ID: store.String(row["id"]), Data: copyFields("steps", row)})
	}
	for _, row := range bundle.Tags {
		input.TagIDs = append(input.TagIDs, store.String(row["id"]))
	}
	return input, bundle
}
func recipeSave(t *testing.T, f *integrationFixture, rid string, input store.RecipeInput) string {
	t.Helper()
	result, err := f.app.Store.SaveRecipe(f.ctx, f.identity, rid, value.UUID(), value.UUID(), input)
	if err != nil {
		t.Fatal(err)
	}
	return store.String(result["id"])
}
func recipeVersionCount(t *testing.T, f *integrationFixture, rid string) int {
	t.Helper()
	var n int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM recipes.versions WHERE recipe_id=$1", rid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecipeWorkspaceAtomicHistoryAndVariant(t *testing.T) {
	f := newIntegrationFixture(t)
	input := recipeInput(t, f)
	tag, _ := auditCreate(t, f, "recipes", "tags", store.Record{"name": "Schnell"})
	input.TagIDs = []string{store.String(tag["id"])}
	rid := recipeSave(t, f, "", input)
	edit, bundle := recipeEdit(t, f, rid)
	if recipeVersionCount(t, f, rid) != 1 || store.String(bundle.Ingredients[0]["quantity"]) != "9007199254.740993" || bundle.Ingredients[1]["quantity"] != nil || bundle.Ingredients[1]["unit_code"] != nil || bundle.Steps[0]["timer_seconds"] != nil {
		t.Fatalf("initial complete recipe: %#v", bundle)
	}
	versions, err := f.app.Store.RecipeVersions(f.ctx, f.identity, rid, 0)
	if err != nil || len(versions) != 1 {
		t.Fatal(versions, err)
	}
	firstVersion := store.String(versions[0]["id"])
	history, err := f.app.Store.RecipeVersion(f.ctx, f.identity, rid, firstVersion)
	if err != nil || store.String(history.Ingredients[0]["quantity"]) != "9007199254.740993" {
		t.Fatal("history lost exact amount", err)
	}
	edit.Data["title"] = json.RawMessage(`"Changed recipe"`)
	edit.Ingredients[0], edit.Ingredients[1] = edit.Ingredients[1], edit.Ingredients[0]
	edit.Steps = edit.Steps[:1]
	key := value.UUID()
	for range 2 {
		if _, err = f.app.Store.SaveRecipe(f.ctx, f.identity, rid, key, value.UUID(), edit); err != nil {
			t.Fatal(err)
		}
	}
	if recipeVersionCount(t, f, rid) != 2 {
		t.Fatal("bulk save or replay produced partial/duplicate versions")
	}
	_, changed := recipeEdit(t, f, rid)
	if store.String(changed.Ingredients[0]["name"]) != "To taste" || len(changed.Steps) != 1 {
		t.Fatal("order/delete not saved")
	}
	var orphanSearch bool
	if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.search_documents WHERE resource_id=$1)", bundle.Steps[1]["id"]).Scan(&orphanSearch); err != nil || orphanSearch {
		t.Fatal("removed child remains searchable", err)
	}
	variant := input
	variant.SourceVersionID = firstVersion
	variant.Data = recipeData(t, store.Record{"title": "Independent variant", "base_servings": "3"})
	variantID := recipeSave(t, f, "", variant)
	_, variantBundle := recipeEdit(t, f, variantID)
	if store.String(variantBundle.Recipe["source_version_id"]) != firstVersion || variantBundle.Ingredients[0]["id"] == bundle.Ingredients[0]["id"] {
		t.Fatal("variant is not independent")
	}
	edit, _ = recipeEdit(t, f, rid)
	edit.Ingredients = nil
	recipeSave(t, f, rid, edit)
	_, variantBundle = recipeEdit(t, f, variantID)
	if len(variantBundle.Ingredients) != 2 {
		t.Fatal("editing source changed variant")
	}
	history, err = f.app.Store.RecipeVersion(f.ctx, f.identity, rid, firstVersion)
	if err != nil || len(history.Ingredients) != 2 || store.String(history.Recipe["title"]) != "Precise recipe" {
		t.Fatal("history was mutated", err)
	}
	for _, query := range []string{"UPDATE recipes.versions SET revision=99 WHERE id=$1", "DELETE FROM recipes.versions WHERE id=$1", "INSERT INTO recipes.versions SELECT * FROM recipes.versions WHERE id=$1"} {
		if _, err = f.pool.Exec(f.ctx, query, firstVersion); err == nil {
			t.Fatal("runtime changed immutable history", query)
		}
	}
}

func TestRecipeWorkspaceConflictsRollbackAndAuthorization(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	foreign := recipeSave(t, f, "", recipeInput(t, f))
	_, foreignBundle := recipeEdit(t, f, foreign)
	for _, kind := range []string{"foreign-child", "duplicate-child", "foreign-tag", "stale-generation", "reader", "locked-child", "precision", "row-identity", "unknown-field"} {
		t.Run(kind, func(t *testing.T) {
			input, before := recipeEdit(t, f, rid)
			n := recipeVersionCount(t, f, rid)
			identity := f.identity
			input.Data["title"] = json.RawMessage(`"Must roll back"`)
			switch kind {
			case "foreign-child":
				input.Ingredients[1].ID = store.String(foreignBundle.Ingredients[0]["id"])
			case "duplicate-child":
				input.Ingredients[1].ID = input.Ingredients[0].ID
			case "foreign-tag":
				input.TagIDs = []string{value.UUID()}
			case "stale-generation":
				input.Generation++
			case "reader":
				identity.Role = "reader"
			case "precision":
				input.Ingredients[1].Data["quantity"] = json.RawMessage(`"0.0000001"`)
			case "row-identity":
				input.Steps[1].Key = input.Steps[0].Key
			case "unknown-field":
				input.Data["created_by"] = json.RawMessage(`"untrusted"`)
			case "locked-child":
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(f.ctx) }()
				if _, err = tx.Exec(f.ctx, "SELECT id FROM recipes.ingredients WHERE id=$1 FOR UPDATE", before.Ingredients[0]["id"]); err != nil {
					t.Fatal(err)
				}
			}
			request := value.UUID()
			if _, err := f.app.Store.SaveRecipe(f.ctx, identity, rid, value.UUID(), request, input); err == nil {
				t.Fatal("invalid recipe accepted")
			}
			_, after := recipeEdit(t, f, rid)
			if after.Fingerprint != before.Fingerprint || recipeVersionCount(t, f, rid) != n {
				t.Fatal("failed write left partial data/history")
			}
			var audits int
			if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE request_id=$1", request).Scan(&audits); err != nil || audits != 0 {
				t.Fatal("failed write left audit", audits, err)
			}
		})
	}
	input, _ := recipeEdit(t, f, rid)
	body, _ := json.Marshal(input)
	for _, tc := range []struct {
		token, csrf string
		want        int
	}{{"", "", http.StatusUnauthorized}, {f.token, "", http.StatusForbidden}} {
		response, _, err := f.request(http.MethodPost, "/api/v1/recipes/"+rid+"/workspace", tc.token, tc.csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
		if err != nil || response.StatusCode != tc.want {
			t.Fatal("recipe security boundary", response, err)
		}
	}
}

func TestRecipeWorkspaceConcurrentAndLegacyEdits(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	input, before := recipeEdit(t, f, rid)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := f.app.Store.SaveRecipe(f.ctx, f.identity, rid, value.UUID(), value.UUID(), input)
			results <- err
		}()
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 || recipeVersionCount(t, f, rid) != 2 {
		t.Fatal("concurrent edit lost update", success, conflicts)
	}
	input, before = recipeEdit(t, f, rid)
	m, _ := f.app.Store.Module("recipes")
	ingredient, _ := m.Entity("ingredients")
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, ingredient, store.String(before.Ingredients[0]["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"name": "Generic edit"}, store.Int(before.Ingredients[0]["version"]))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.SaveRecipe(f.ctx, f.identity, rid, value.UUID(), value.UUID(), input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("generic child change not detected", err)
	}
	versions, err := f.app.Store.RecipeVersions(f.ctx, f.identity, rid, 0)
	if err != nil || len(versions) != 3 {
		t.Fatal("generic edit not versioned", versions, err)
	}
	latest, err := f.app.Store.RecipeVersion(f.ctx, f.identity, rid, store.String(versions[0]["id"]))
	if err != nil || store.String(latest.Ingredients[0]["name"]) != "Generic edit" {
		t.Fatal("generic edit snapshot incomplete", err)
	}
	tag, _ := auditCreate(t, f, "recipes", "tags", store.Record{"name": "Before tag"})
	input, _ = recipeEdit(t, f, rid)
	input.TagIDs = []string{store.String(tag["id"])}
	recipeSave(t, f, rid, input)
	input, _ = recipeEdit(t, f, rid)
	tags, _ := m.Entity("tags")
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, tags, store.String(tag["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"name": "Renamed tag"}, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.SaveRecipe(f.ctx, f.identity, rid, value.UUID(), value.UUID(), input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("tag rename invisible to conflict check", err)
	}
	n := recipeVersionCount(t, f, rid)
	if err = f.app.Store.Delete(f.ctx, f.identity, m, tags, store.String(tag["id"]), 2, input.Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if recipeVersionCount(t, f, rid) != n+1 {
		t.Fatal("tag deletion lost history")
	}
	versions, err = f.app.Store.RecipeVersions(f.ctx, f.identity, rid, 0)
	if err != nil {
		t.Fatal(err)
	}
	latest, err = f.app.Store.RecipeVersion(f.ctx, f.identity, rid, store.String(versions[0]["id"]))
	if err != nil || len(latest.Tags) != 0 {
		t.Fatal("tag deletion snapshot stale", err)
	}
}

func TestRecipeWorkspaceBoundedBundleAndHistoryPages(t *testing.T) {
	f := newIntegrationFixture(t)
	input := recipeInput(t, f)
	input.Ingredients = nil
	for n := 0; n < 500; n++ {
		input.Ingredients = append(input.Ingredients, store.RecipeRow{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Bounded ingredient", "quantity": "0.000001", "unit_code": "g"})})
	}
	rid := recipeSave(t, f, "", input)
	edit, bundle := recipeEdit(t, f, rid)
	if len(bundle.Ingredients) != 500 {
		t.Fatal("bundle silently truncated", len(bundle.Ingredients))
	}
	edit.Ingredients = append(edit.Ingredients, store.RecipeRow{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Too many"})})
	if _, err := f.app.Store.SaveRecipe(f.ctx, f.identity, rid, value.UUID(), value.UUID(), edit); err == nil {
		t.Fatal("501 ingredients accepted")
	}
	m, _ := f.app.Store.Module("recipes")
	ingredients, _ := m.Entity("ingredients")
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, ingredients, "", value.UUID(), value.UUID(), auditMutation(t, f, "recipes", store.Record{"recipe_id": rid, "name": "Generic overflow"}, 0)); err == nil {
		t.Fatal("generic path bypassed size contract")
	}
	edit, _ = recipeEdit(t, f, rid)
	edit.Ingredients = nil
	recipeSave(t, f, rid, edit)
	for range 23 {
		edit, _ = recipeEdit(t, f, rid)
		recipeSave(t, f, rid, edit)
	}
	page, err := f.app.Store.RecipeVersions(f.ctx, f.identity, rid, 0)
	if err != nil || len(page) != 21 {
		t.Fatal("history page bound", len(page), err)
	}
	next, err := f.app.Store.RecipeVersions(f.ctx, f.identity, rid, store.Int(page[19]["revision"]))
	if err != nil || len(next) != 5 || store.Int(next[0]["revision"]) >= store.Int(page[19]["revision"]) {
		t.Fatal("history cursor repeated or skipped", next, err)
	}
}

func TestRecipeVersionsPopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		switch schema {
		case "core":
			migratePriorAuditSchema(t, f, schema, 10)
		case "shopping":
			migratePriorAuditSchema(t, f, schema, 4)
		case "pantry":
			migratePriorAuditSchema(t, f, schema, 2)
		default:
			migratePriorAuditSchema(t, f, schema)
		}
	}
	hid, uid, rid := value.UUID(), value.UUID(), value.UUID()
	statements := []struct {
		sql  string
		args []any
	}{{"INSERT INTO core.households(id,name) VALUES($1,'Prior recipes')", []any{hid}}, {"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-recipes','Prior','fixture-no-login')", []any{uid}}, {"INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES($1,$2,'Old recipe',2.500001,$3)", []any{rid, hid, uid}}, {"INSERT INTO recipes.ingredients(household_id,recipe_id,name,quantity,created_by) VALUES($1,$2,'Exact',9007199254.740993,$3)", []any{hid, rid, uid}}}
	for _, statement := range statements {
		if _, err := f.owner.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	// Reproduce the old child-first writer order while migration waits at the
	// household barrier. It must finish before any parent/table migration lock.
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	writer, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err = writer.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", hid); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(ctx, "SELECT id FROM recipes.ingredients WHERE recipe_id=$1 FOR UPDATE", rid); err != nil {
		t.Fatal(err)
	}
	migrated := make(chan error, 1)
	go func() { migrated <- database.Migrate(ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")) }()
	blocked := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.datname=current_database() AND NOT l.granted AND a.query LIKE '%Match BeginMutation%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("migration never reached household barrier")
	}
	if _, err = writer.Exec(ctx, "SELECT id FROM recipes.recipes WHERE id=$1 FOR UPDATE", rid); err != nil {
		t.Fatal("migration inverted legacy child/parent lock", err)
	}
	if _, err = writer.Exec(ctx, "UPDATE recipes.ingredients SET quantity=9007199254.740993 WHERE recipe_id=$1", rid); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-migrated; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var exact string
	var honest bool
	err = f.owner.QueryRow(f.ctx, "SELECT count(*),min(snapshot->'ingredients'->0->>'quantity'),bool_and(created_by IS NULL AND revision=1) FROM recipes.versions WHERE recipe_id=$1", rid).Scan(&count, &exact, &honest)
	if err != nil || count != 1 || exact != "9007199254.740993" || !honest {
		t.Fatal("populated baseline changed or fabricated history", count, exact, honest, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeVariantRejectsConcurrentSourceDeletion(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	versions, err := f.app.Store.RecipeVersions(f.ctx, f.identity, rid, 0)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "DELETE FROM recipes.recipes WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	input := recipeInput(t, f)
	input.SourceVersionID = store.String(versions[0]["id"])
	if _, err = f.app.Store.SaveRecipe(f.ctx, f.identity, "", value.UUID(), value.UUID(), input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("variant copied a concurrently deleted source", err)
	}
}

func TestRecipeVersionsRestoredTransactionIDCollision(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "recipes", f.app.Store.States()["recipes"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var xid int64
	if err = tx.QueryRow(f.ctx, "SELECT txid_current()").Scan(&xid); err != nil {
		t.Fatal(err)
	}
	// Model an imported history row whose old-cluster XID equals this new TX.
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO recipes.versions(household_id,recipe_id,revision,transaction_id,transaction_started_at,snapshot) VALUES($1,$2,2,$3,'2000-01-01',recipes.content_snapshot($1,$2))`, f.identity.HouseholdID, rid, xid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE recipes.recipes SET title='Edit after restored XID collision',version=version+1 WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var revisions int
	var latest string
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*),max(snapshot->'recipe'->>'title') FILTER(WHERE revision=3) FROM recipes.versions WHERE recipe_id=$1", rid).Scan(&revisions, &latest); err != nil || revisions != 3 || latest != "Edit after restored XID collision" {
		t.Fatal("restored XID suppressed new history", revisions, latest, err)
	}
}
