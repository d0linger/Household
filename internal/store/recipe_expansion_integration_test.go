//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/database"
	"household/internal/modules"
	"household/internal/recipeimport"
	"household/internal/value"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

const recipeExpansionPassword = "Recipe expansion fixture 1234"

type recipeExpansionFixture struct {
	ctx      context.Context
	ownerURL string
	owner    *pgxpool.Pool
	app      *pgxpool.Pool
	store    *Store
	identity auth.Identity
}

// The fixture owns only household_test, after checking both actual roles and
// taking the same exclusive advisory lock used by the web integration suite.
func newRecipeExpansionFixture(t *testing.T) *recipeExpansionFixture {
	t.Helper()
	return newRecipeExpansionFixtureForProfile(t, false)
}

func newRecipeExpansionPredecessorFixture(t *testing.T) *recipeExpansionFixture {
	t.Helper()
	return newRecipeExpansionFixtureForProfile(t, true)
}

func newRecipeExpansionFixtureForProfile(t *testing.T, predecessor bool) *recipeExpansionFixture {
	t.Helper()
	if predecessor {
		return newRecipeExpansionFixtureForMigration(t, recipeExpansionMigratePredecessor)
	}
	return newRecipeExpansionFixtureForMigration(t, nil)
}

func newRecipeExpansionFixtureForMigration(t *testing.T, profile func(*testing.T, context.Context, string, *pgxpool.Pool)) *recipeExpansionFixture {
	t.Helper()
	ownerURL, appURL := os.Getenv("HOUSEHOLD_TEST_OWNER_URL"), os.Getenv("HOUSEHOLD_TEST_APP_URL")
	if ownerURL == "" || appURL == "" {
		if os.Getenv("HOUSEHOLD_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("isolated household test owner and app URLs are required")
		}
		t.Skip("isolated household test URLs are required")
	}
	ownerCfg, ownerErr := pgx.ParseConfig(ownerURL)
	appCfg, appErr := pgx.ParseConfig(appURL)
	if ownerErr != nil || appErr != nil || ownerCfg.Database != "household_test" ||
		appCfg.Database != "household_test" || ownerCfg.User != "household_owner" ||
		appCfg.User != "household_app" || ownerCfg.Host != appCfg.Host ||
		ownerCfg.Port != appCfg.Port ||
		(ownerCfg.Host != "127.0.0.1" && ownerCfg.Host != "localhost") ||
		(ownerCfg.Port != 55434 && (ownerCfg.Port != 5432 || os.Getenv("CI") != "true")) {
		t.Fatal("refusing integration work outside isolated household_test roles and endpoint")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	owner, err := database.Open(ctx, ownerURL, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	app, err := database.Open(ctx, appURL, 8)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HOUSEHOLD_TEST_QUERY_DIAGNOSTICS") == "1" {
		cfg := app.Config()
		app.Close()
		cfg.ConnConfig.Tracer = recipeExpansionQueryDiagnostics{t: t}
		app, err = pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal("open diagnostic test pool:", err)
		}
	}
	t.Cleanup(app.Close)
	for _, entry := range []struct {
		pool *pgxpool.Pool
		role string
	}{{owner, "household_owner"}, {app, "household_app"}} {
		var actualDB, actualRole string
		if err = entry.pool.QueryRow(ctx, "SELECT current_database(),current_user").
			Scan(&actualDB, &actualRole); err != nil || actualDB != "household_test" || actualRole != entry.role {
			t.Fatal("actual database role guard failed")
		}
	}
	lock, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var locked bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(726241999)").Scan(&locked); err != nil || !locked {
		lock.Release()
		t.Fatal("isolated test database is owned by another integration fixture")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := lock.Exec(cleanup, "SELECT pg_advisory_unlock(726241999)"); err != nil {
			t.Errorf("release integration lock: %v", err)
		}
		lock.Release()
	})
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("HOUSEHOLD_TEST_RETAIN_FAILED_FIXTURE") == "1" {
			t.Log("diagnostic only: failed isolated fixture retained; roles and advisory lock still released")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := recipeExpansionReset(cleanup, owner); err != nil {
			t.Errorf("clean isolated recipe fixture: %v", err)
		}
	})
	if err = recipeExpansionReset(ctx, owner); err != nil {
		t.Fatal("reset isolated fixture before migration:", err)
	}
	if profile != nil {
		profile(t, ctx, ownerURL, owner)
	} else if err = database.Migrate(ctx, ownerURL); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		EncryptionKey: auth.RandomToken(), RateSecret: auth.RandomToken(),
		CSRFSecret: auth.RandomToken(), AdminUsername: "recipe-expansion",
		AdminPassword: recipeExpansionPassword, HouseholdName: "Recipe test",
		SessionIdle: 24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour,
	}
	a, err := auth.New(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mods := modules.Registry()
	ids, enabled := make([]string, 0, len(mods)), map[string]bool{}
	for _, descriptor := range mods {
		ids = append(ids, descriptor.ID)
		enabled[descriptor.ID] = true
	}
	if err = a.Bootstrap(ctx, ids, enabled); err != nil {
		t.Fatal("bootstrap isolated fixture:", err)
	}
	if _, err = owner.Exec(ctx, "UPDATE core.users SET must_change_password=false WHERE username=$1", cfg.AdminUsername); err != nil {
		t.Fatal("set isolated bootstrap password state:", err)
	}
	_, identity, err := a.Login(ctx, cfg.AdminUsername, recipeExpansionPassword, "", "127.0.0.1", "Recipe test")
	if err != nil {
		t.Fatal("login isolated fixture:", err)
	}
	s := New(app, mods)
	if err = s.LoadStates(ctx); err != nil {
		t.Fatal("load isolated module states:", err)
	}
	return &recipeExpansionFixture{ctx: ctx, ownerURL: ownerURL, owner: owner, app: app,
		store: s, identity: identity}
}

type recipeExpansionQueryDiagnostics struct{ t *testing.T }
type recipeExpansionQueryTraceKey struct{}
type recipeExpansionQueryTrace struct {
	started time.Time
	label   string
}

func (d recipeExpansionQueryDiagnostics) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	label := strings.Join(strings.Fields(data.SQL), " ")
	if len(label) > 90 {
		label = label[:90]
	}
	return context.WithValue(ctx, recipeExpansionQueryTraceKey{}, recipeExpansionQueryTrace{time.Now(), label})
}

func (d recipeExpansionQueryDiagnostics) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	trace, ok := ctx.Value(recipeExpansionQueryTraceKey{}).(recipeExpansionQueryTrace)
	if !ok {
		return
	}
	elapsed := time.Since(trace.started)
	if elapsed >= 100*time.Millisecond || data.Err != nil || os.Getenv("HOUSEHOLD_TEST_QUERY_DIAGNOSTICS") == "1" {
		d.t.Logf("isolated query diagnostic %s %q", elapsed, trace.label)
		if problem, ok := errors.AsType[*pgconn.PgError](data.Err); ok {
			d.t.Logf("isolated query failure SQLSTATE=%s message=%s context=%s", problem.Code, problem.Message, problem.Where)
		}
	}
}

func recipeExpansionReset(ctx context.Context, owner *pgxpool.Pool) error {
	tx, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Preserve atomic reset and the existing statement/total deadlines. Match
	// the accepted Web fixture: optional schemas first, shared Core last.
	for n := len(database.Schemas) - 1; n >= 0; n-- {
		schema := pgx.Identifier{database.Schemas[n]}.Sanitize()
		if _, err = tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func TestRecipeExpansionFreshReadinessAndImportPurpose(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	if err := database.Ready(f.ctx, f.app); err != nil {
		t.Fatal("fresh readiness", err)
	}
	var coreReady, recipesReady, audited, fuelReady bool
	var policies int
	if err := f.app.QueryRow(f.ctx, `SELECT core.recipe_expansion_ready(),recipes.expansion_ready(),
 core.audit_ready(),fuel.module_ready()`).Scan(&coreReady, &recipesReady, &audited, &fuelReady); err != nil ||
		!coreReady || !recipesReady || !audited || !fuelReady {
		t.Fatalf("fresh contracts core=%t recipes=%t audit=%t fuel=%t error=%v",
			coreReady, recipesReady, audited, fuelReady, err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_policies`).Scan(&policies); err != nil || policies != 152 {
		t.Fatalf("fresh audit policy count %d: %v", policies, err)
	}
	var teaspoons, tablespoons bool
	if err := f.app.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.units
 WHERE code='tsp' AND dimension='volume' AND factor_to_base=5),
 EXISTS(SELECT 1 FROM core.units WHERE code='tbsp' AND dimension='volume' AND factor_to_base=15)`).
		Scan(&teaspoons, &tablespoons); err != nil || !teaspoons || !tablespoons {
		t.Fatalf("exact kitchen units %t/%t: %v", teaspoons, tablespoons, err)
	}
	var flatID, purpose string
	if err := f.owner.QueryRow(f.ctx, `INSERT INTO core.import_batches
 (household_id,module_id,module_generation,entity,row_count,valid_count,error_count)
 VALUES($1,'recipes',$2,'recipes',1,1,0) RETURNING id,purpose`,
		f.identity.HouseholdID, f.store.States()["recipes"].Generation).
		Scan(&flatID, &purpose); err != nil || purpose != "flat_csv" {
		t.Fatalf("legacy flat import purpose %q: %v", purpose, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.import_batches SET purpose='recipe_review' WHERE id=$1", flatID); err == nil {
		t.Fatal("flat CSV batch changed purpose without review authority")
	}
	digest := sha256.Sum256([]byte("reviewed recipe fixture"))
	var reviewID string
	if err := f.owner.QueryRow(f.ctx, `INSERT INTO core.import_batches
 (household_id,module_id,module_generation,entity,row_count,valid_count,error_count,
 purpose,owner_user_id,expires_at,preview_sha256)
 VALUES($1,'recipes',$2,'recipes',1,1,0,'recipe_review',$3,clock_timestamp()+interval '25 minutes',$4)
 RETURNING id`, f.identity.HouseholdID, f.store.States()["recipes"].Generation,
		f.identity.UserID, digest[:]).Scan(&reviewID); err != nil {
		t.Fatal("valid bounded review batch", err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.import_batches SET purpose='flat_csv' WHERE id=$1", reviewID); err == nil {
		t.Fatal("review batch changed purpose after creation")
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.import_batches
 (household_id,module_id,module_generation,entity,row_count,valid_count,error_count,purpose,
 owner_user_id,expires_at,preview_sha256)
 VALUES($1,'recipes',$2,'recipes',21,21,0,'recipe_review',$3,
 clock_timestamp()+interval '25 minutes',$4)`, f.identity.HouseholdID,
		f.store.States()["recipes"].Generation, f.identity.UserID, digest[:]); err == nil {
		t.Fatal("over-bound recipe review batch was accepted")
	}
}

func TestRecipeExpansionImportDuplicatePreflight(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	const title = "Overbroad duplicate fixture"
	for range 6 {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.recipes
 (household_id,title,base_servings,created_by) VALUES($1,$2,2,$3)`,
			f.identity.HouseholdID, title, f.identity.UserID); err != nil {
			t.Fatal("seed matching recipe", err)
		}
	}
	var before, after int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.import_batches
 WHERE household_id=$1 AND purpose='recipe_review'`, f.identity.HouseholdID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("bounded duplicate preview"))
	_, err := f.store.CreateRecipeImportPreview(f.ctx, f.identity,
		f.store.States()["recipes"].Generation, "household-v1", "",
		hex.EncodeToString(digest[:]), []recipeimport.Recipe{{Title: title, Servings: "2"}})
	var invalid Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("overbroad duplicate preview was accepted: %v", err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.import_batches
 WHERE household_id=$1 AND purpose='recipe_review'`, f.identity.HouseholdID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("unreviewable preview batch persisted: %d -> %d", before, after)
	}
}

func TestRecipeExpansionImportReceiptSurvivesPreviewCleanup(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	generation := f.store.States()["recipes"].Generation
	source := sha256.Sum256([]byte("receipt preview fixture"))
	preview, err := f.store.CreateRecipeImportPreview(f.ctx, f.identity, generation,
		"household-v1", "", hex.EncodeToString(source[:]), []recipeimport.Recipe{{
			Title: "Durable import receipt fixture", Servings: "2",
			Ingredients: []recipeimport.Ingredient{{Name: "Flour", Quantity: "100", Unit: "g"}},
			Steps:       []recipeimport.Step{{Instruction: "Mix"}},
		}})
	if err != nil || len(preview.Rows) != 1 {
		t.Fatalf("create one-row review preview: %v/%v", preview, err)
	}
	key := value.UUID()
	decision := RecipeImportDecision{Decision: "create", Generation: generation,
		PreviewDigest: preview.PreviewDigest}
	confirmed, err := f.store.ConfirmRecipeImportRow(f.ctx, f.identity,
		preview.ID, 1, key, value.UUID(), decision)
	if err != nil || !value.ValidUUID(String(confirmed["id"])) {
		t.Fatalf("confirm reviewed recipe: %v/%v", confirmed, err)
	}
	if _, err = f.owner.Exec(f.ctx, `DELETE FROM core.import_batches WHERE id=$1
 AND household_id=$2 AND purpose='recipe_review'`, preview.ID, f.identity.HouseholdID); err != nil {
		t.Fatal("simulate expired preview cleanup", err)
	}
	replayed, err := f.store.ConfirmRecipeImportRow(f.ctx, f.identity,
		preview.ID, 1, key, value.UUID(), decision)
	if err != nil || String(replayed["id"]) != String(confirmed["id"]) {
		t.Fatalf("same-key receipt vanished with preview: %v/%v", replayed, err)
	}
	changed := decision
	changed.PreviewDigest = strings.Repeat("0", sha256.Size*2)
	if _, err = f.store.ConfirmRecipeImportRow(f.ctx, f.identity,
		preview.ID, 1, key, value.UUID(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed same-key decision replay was accepted: %v", err)
	}
	changed = decision
	changed.Generation++
	if _, err = f.store.ConfirmRecipeImportRow(f.ctx, f.identity,
		preview.ID, 1, key, value.UUID(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("previous-generation receipt was accepted: %v", err)
	}
	if _, err = f.store.ConfirmRecipeImportRow(f.ctx, f.identity,
		preview.ID, 1, value.UUID(), value.UUID(), decision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new key reused an expired review: %v", err)
	}
	var recipes int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM recipes.recipes
 WHERE household_id=$1 AND title='Durable import receipt fixture'`, f.identity.HouseholdID).
		Scan(&recipes); err != nil || recipes != 1 {
		t.Fatalf("replay duplicated the committed recipe: %d/%v", recipes, err)
	}
}

func TestRecipeExpansionLegacyRecipeRemainsEditable(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	rid, ingredientID, stepID := value.UUID(), value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.recipes
 (id,household_id,title,base_servings,prep_minutes,created_by)
 VALUES($1,$2,'Legacy generous recipe',2,1000000000,$3)`,
		rid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal("seed valid older recipe", err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.ingredients
 (id,household_id,recipe_id,position,name,quantity,unit_code,created_by)
 VALUES($1,$2,$3,1,'Legacy ingredient',100000000000,'g',$4)`,
		ingredientID, f.identity.HouseholdID, rid, f.identity.UserID); err != nil {
		t.Fatal("seed valid large ingredient", err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.steps
 (id,household_id,recipe_id,position,instruction,timer_seconds,created_by)
 VALUES($1,$2,$3,1,'Wait for the old timer',1000000000,$4)`,
		stepID, f.identity.HouseholdID, rid, f.identity.UserID); err != nil {
		t.Fatal("seed valid large timer", err)
	}
	before, err := f.store.Recipe(f.ctx, f.identity, rid)
	if err != nil || len(before.Ingredients) != 1 || len(before.Steps) != 1 {
		t.Fatalf("load legacy recipe: %v", err)
	}
	input := importedRecipeInput(recipeimport.Recipe{
		Title: "Renamed legacy recipe", Servings: String(before.Recipe["base_servings"]),
		PrepMinutes: int(Int(before.Recipe["prep_minutes"])),
		Ingredients: []recipeimport.Ingredient{{Name: "Legacy ingredient",
			Quantity: String(before.Ingredients[0]["quantity"]), Unit: "g"}},
		Steps: []recipeimport.Step{{Instruction: "Wait for the old timer",
			TimerSeconds: int(Int(before.Steps[0]["timer_seconds"]))}},
	}, f.store.States()["recipes"].Generation, before.Fingerprint, nil)
	input.Ingredients[0].ID = ingredientID
	input.Steps[0].ID = stepID
	if _, err = f.store.SaveRecipe(f.ctx, f.identity, rid, value.UUID(), value.UUID(), input); err != nil {
		t.Fatalf("title edit on valid legacy recipe was blocked: %v", err)
	}
	after, err := f.store.Recipe(f.ctx, f.identity, rid)
	if err != nil || len(after.Ingredients) != 1 || len(after.Steps) != 1 ||
		String(after.Recipe["title"]) != "Renamed legacy recipe" ||
		String(after.Ingredients[0]["quantity"]) != "100000000000.000000" ||
		Int(after.Steps[0]["timer_seconds"]) != 1000000000 {
		t.Fatalf("legacy contents were changed by title edit: %v/%v", after, err)
	}
	_, err = f.store.RecipePortable(f.ctx, f.identity, rid)
	var invalid Invalid
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "portable") {
		t.Fatalf("strict portable export did not explain legacy bound: %v", err)
	}
}

func TestRecipeExpansionInsightsAndGuards(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	recipeGeneration := f.store.States()["recipes"].Generation
	shoppingGeneration := f.store.States()["shopping"].Generation
	sourceCatalog, targetCatalog := value.UUID(), value.UUID()
	for _, entry := range []struct{ id, name string }{
		{sourceCatalog, "Fixture flour"}, {targetCatalog, "Fixture replacement"},
	} {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.ingredient_catalog
 (id,household_id,name,created_by) VALUES($1,$2,$3,$4)`,
			entry.id, f.identity.HouseholdID, entry.name, f.identity.UserID); err != nil {
			t.Fatal("seed catalogue", err)
		}
	}
	input := importedRecipeInput(recipeimport.Recipe{
		Title: "Exact recipe fixture", Servings: "2",
		Ingredients: []recipeimport.Ingredient{{Name: "Fixture flour", Quantity: "100", Unit: "g"}},
		Steps:       []recipeimport.Step{{Instruction: "Mix fixture"}},
	}, recipeGeneration, "", nil)
	created, err := f.store.SaveRecipe(f.ctx, f.identity, "", value.UUID(), value.UUID(), input)
	if err != nil {
		t.Fatal("create bounded recipe", err)
	}
	rid := String(created["id"])
	bundle, err := f.store.Recipe(f.ctx, f.identity, rid)
	if err != nil || len(bundle.Ingredients) != 1 {
		t.Fatal("load one recipe ingredient", err)
	}
	ingredientID := String(bundle.Ingredients[0]["id"])
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.ingredients SET catalog_id=$3,
 version=version+1 WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, ingredientID, sourceCatalog); err != nil {
		t.Fatal("assign explicit recipe catalogue", err)
	}
	storeID, productID := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO shopping.stores(id,household_id,name,created_by)
 VALUES($1,$2,'Fixture shop',$3)`, storeID, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal("seed shopping store", err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO shopping.products
 (id,household_id,name,pack_quantity,pack_unit,created_by)
 VALUES($1,$2,'Fixture flour pack',200,'g',$3)`, productID, f.identity.HouseholdID,
		f.identity.UserID); err != nil {
		t.Fatal("seed shopping product", err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO shopping.price_observations
 (household_id,product_id,store_id,observed_on,quantity,unit_code,price_minor,created_by)
 VALUES($1,$2,$3,CURRENT_DATE,200,'g',1000,$4)`, f.identity.HouseholdID,
		productID, storeID, f.identity.UserID); err != nil {
		t.Fatal("seed dated price", err)
	}
	bound, err := f.store.SaveRecipeShoppingBinding(f.ctx, f.identity, sourceCatalog,
		value.UUID(), value.UUID(), RecipePriceBindingInput{
			Generation: recipeGeneration, ShoppingGeneration: shoppingGeneration,
			CatalogVersion: 1, ProductID: productID, ProductVersion: 1,
		})
	if err != nil || bound["assigned"] != true {
		t.Fatalf("explicit current Shopping binding: %v/%v", bound, err)
	}
	insights, err := f.store.RecipeInsights(f.ctx, f.identity, rid, "")
	if err != nil {
		t.Fatal("read recipe price insights", err)
	}
	cost := insights["cost"].(Record)
	if cost["known_minor"] != "500" || cost["unknown_count"] != 0 {
		t.Fatalf("exact partial cost: %v", cost)
	}
	lines := cost["lines"].([]Record)
	if len(lines) != 1 || lines[0]["status"] != "priced" ||
		String(lines[0]["binding_id"]) == "" || String(lines[0]["observed_on"]) == "" {
		t.Fatalf("cost provenance and binding review tokens: %v", lines)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE shopping.price_observations SET price_minor=1000000000000
 WHERE household_id=$1 AND product_id=$2`, f.identity.HouseholdID, productID); err != nil {
		t.Fatal("set highest valid observed price", err)
	}
	maxPriceInsights, err := f.store.RecipeInsights(f.ctx, f.identity, rid, "")
	if err != nil || maxPriceInsights["cost"].(Record)["known_minor"] != "500000000000" {
		t.Fatalf("highest valid bigint price was lost: %v/%v", maxPriceInsights, err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE shopping.price_observations SET price_minor=1000
 WHERE household_id=$1 AND product_id=$2`, f.identity.HouseholdID, productID); err != nil {
		t.Fatal("restore observed price", err)
	}
	if _, err = f.store.SaveRecipeShoppingBinding(f.ctx, f.identity, sourceCatalog,
		value.UUID(), value.UUID(), RecipePriceBindingInput{
			Generation: recipeGeneration, ShoppingGeneration: shoppingGeneration + 1,
			CatalogVersion: 1, BindingID: String(bound["id"]), Version: Int(bound["version"]),
			ProductID: productID, ProductVersion: 1,
		}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Shopping generation accepted: %v", err)
	}
	bundle, err = f.store.Recipe(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SaveRecipeProfile(f.ctx, f.identity, rid, value.UUID(), value.UUID(),
		RecipeProfileInput{Generation: recipeGeneration, Version: Int(bundle.Recipe["version"]),
			Fingerprint: bundle.Fingerprint, Basis: "per_recipe", BasisServings: "2",
			Source: "Fixture label", SourceDate: "2026-10-30",
			Nutrients: map[string]string{"protein_g": "10"},
			Allergens: map[string]string{"milk": "contains"}})
	if err != nil {
		t.Fatal("save explicitly sourced nutrition", err)
	}
	insights, err = f.store.RecipeInsights(f.ctx, f.identity, rid, "1")
	if err != nil {
		t.Fatal(err)
	}
	nutrition := insights["nutrition"].(Record)
	if nutrition["stale"] != false || nutrition["scaled_nutrients"].(Record)["protein_g"].(RecipeConvertedQuantity).Display != "5" ||
		insights["allergens"].(Record)["status_by_code"].(Record)["milk"] != "contains" {
		t.Fatalf("current explicit profile and half yield: %v", nutrition)
	}
	_, err = f.store.SaveRecipeSubstitutions(f.ctx, f.identity, sourceCatalog,
		value.UUID(), value.UUID(), RecipeSubstitutionInput{
			Generation: recipeGeneration, CatalogVersion: 1,
			Rules: []RecipeSubstitutionRule{{TargetCatalogID: targetCatalog, Ratio: "2",
				SourceUnitCode: "g", UnitCode: "piece", Source: "Fixture rule",
				SourceDate: "2026-10-30", Note: "Reviewed"}},
		})
	if err != nil {
		t.Fatal("save source-unit-specific substitution", err)
	}
	_, err = f.store.SaveRecipeDensity(f.ctx, f.identity, sourceCatalog,
		value.UUID(), value.UUID(), RecipeDensityInput{Generation: recipeGeneration,
			CatalogVersion: 2, GramsPerML: "0.8", Source: "Fixture density",
			SourceDate: "2026-10-30"})
	if err != nil {
		t.Fatal("save sourced ingredient density", err)
	}
	insights, err = f.store.RecipeInsights(f.ctx, f.identity, rid, "2")
	if err != nil {
		t.Fatal(err)
	}
	groups := insights["substitutions"].([]Record)
	if len(groups) != 1 || groups[0]["catalog_version"] != json.Number("3") {
		t.Fatalf("current substitution review group: %v", groups)
	}
	rules := groups[0]["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["status"] != "current" ||
		rules[0].(map[string]any)["source_unit_code"] != "g" {
		t.Fatalf("source-unit substitution status: %v", rules)
	}
	foundVolume := false
	for _, conversion := range insights["conversions"].([]Record) {
		if conversion["ingredient_id"] == ingredientID && conversion["target_unit"] == "ml" {
			foundVolume = conversion["status"] == "exact" && conversion["quantity"] == "125"
		}
	}
	if !foundVolume {
		t.Fatal("100 g at 0.8 g/ml did not yield exact 125 ml")
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.recipes SET nutrition_profile=$3::jsonb
 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, rid,
		`{"basis":null,"basis_servings":"2","source":"Fixture","source_date":"2026-10-30",`+
			`"ingredient_fingerprint":"0000000000000000000000000000000000000000000000000000000000000000",`+
			`"nutrients":{},"allergens":{}}`); err == nil {
		t.Fatal("JSON null bypassed sourced nutrition basis constraint")
	}
	invalidRule, err := json.Marshal([]Record{{"target_catalog_id": targetCatalog,
		"ratio": "1", "source_unit_code": nil, "unit_code": "g", "source": "Fixture",
		"source_date": "2026-10-30", "note": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.ingredient_catalog
 SET substitution_rules=$3::jsonb WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, sourceCatalog, invalidRule); err == nil {
		t.Fatal("null substitution source unit bypassed bounded rule constraint")
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.ingredient_catalog
 SET density_g_per_ml=0 WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, sourceCatalog); err == nil {
		t.Fatal("zero density bypassed sourced density constraint")
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.ingredients SET quantity=120,
 version=version+1 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, ingredientID); err != nil {
		t.Fatal("alter evaluated ingredient", err)
	}
	insights, err = f.store.RecipeInsights(f.ctx, f.identity, rid, "2")
	if err != nil {
		t.Fatal(err)
	}
	if insights["nutrition"].(Record)["stale"] != true ||
		insights["allergens"].(Record)["status_by_code"].(Record)["milk"] != "unknown" ||
		insights["cost"].(Record)["known_minor"] != "600" {
		t.Fatalf("ingredient change did not stale source and recalculate price: %v", insights)
	}
	forgedProvenance, err := json.Marshal(Record{
		"kind": "density", "source_catalog_id": sourceCatalog,
		"target_catalog_id": sourceCatalog, "source_name": "Fixture flour",
		"source_quantity": "120", "source_unit_code": "g", "density_g_per_ml": "0.800000",
		"source": "Fixture density", "source_date": "2026-10-30",
		"recorded_at": "2026-10-30T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.ingredients
 SET conversion_provenance=$3::jsonb WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, ingredientID, forgedProvenance); err == nil {
		t.Fatal("unchanged ingredient accepted fabricated conversion provenance")
	}
	forgedSource := Record{
		"kind": "density", "source_catalog_id": sourceCatalog,
		"target_catalog_id": sourceCatalog, "source_name": "Fixture flour",
		"source_quantity": "119.000000", "source_unit_code": "g",
		"density_g_per_ml": "0.800000", "source": "Fixture density",
		"source_date": "2026-10-30", "recorded_at": "2026-10-30T12:00:00Z",
	}
	forgedSourceRaw, err := json.Marshal(forgedSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.ingredients SET quantity=125,
 conversion_provenance=$3::jsonb WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, ingredientID, forgedSourceRaw); err == nil {
		t.Fatal("changed ingredient accepted provenance that misstated its original quantity")
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO recipes.ingredients
 (household_id,recipe_id,position,name,quantity,unit_code,catalog_id,conversion_provenance,created_by)
 VALUES($1,$2,2,'Forged insertion',10,'g',$3,$4::jsonb,$5)`,
		f.identity.HouseholdID, rid, sourceCatalog, forgedSourceRaw, f.identity.UserID); err == nil {
		t.Fatal("new ingredient accepted a fabricated conversion source")
	}
	for _, example := range []struct {
		name, targetName, targetCatalog, targetQuantity, targetUnit string
		provenance                                                  Record
	}{
		{"wrong substitution ratio", "Fixture replacement", targetCatalog, "241", "piece", Record{
			"kind": "substitution", "source_catalog_id": sourceCatalog,
			"target_catalog_id": targetCatalog, "source_name": "Fixture flour",
			"source_quantity": "120.000000", "source_unit_code": "g",
			"ratio": "2", "rule_version": "3", "source": "Fixture rule",
			"source_date": "2026-10-30", "recorded_at": "2026-10-30T12:00:00Z"}},
		{"wrong density amount", "Fixture flour", sourceCatalog, "149", "ml", Record{
			"kind": "density", "source_catalog_id": sourceCatalog,
			"target_catalog_id": sourceCatalog, "source_name": "Fixture flour",
			"source_quantity": "120.000000", "source_unit_code": "g",
			"density_g_per_ml": "0.800000", "source": "Fixture density",
			"source_date": "2026-10-30", "recorded_at": "2026-10-30T12:00:00Z"}},
		{"density on same dimension", "Fixture flour", sourceCatalog, "0.120000", "kg", Record{
			"kind": "density", "source_catalog_id": sourceCatalog,
			"target_catalog_id": sourceCatalog, "source_name": "Fixture flour",
			"source_quantity": "120.000000", "source_unit_code": "g",
			"density_g_per_ml": "0.800000", "source": "Fixture density",
			"source_date": "2026-10-30", "recorded_at": "2026-10-30T12:00:00Z"}},
	} {
		t.Run(example.name, func(t *testing.T) {
			raw, marshalErr := json.Marshal(example.provenance)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, updateErr := f.owner.Exec(f.ctx, `UPDATE recipes.ingredients
 SET name=$3,catalog_id=$4,quantity=$5::numeric,unit_code=$6,conversion_provenance=$7::jsonb
 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, ingredientID,
				example.targetName, example.targetCatalog, example.targetQuantity,
				example.targetUnit, raw); updateErr == nil {
				t.Fatal("false exact conversion provenance was accepted")
			}
		})
	}
	if _, err = f.store.SaveRecipeDensity(f.ctx, f.identity, sourceCatalog,
		value.UUID(), value.UUID(), RecipeDensityInput{Generation: recipeGeneration,
			CatalogVersion: 2, GramsPerML: "1", Source: "Stale",
			SourceDate: "2026-10-30"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale catalogue version accepted: %v", err)
	}
	bundle, err = f.store.Recipe(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal("load recipe before archive boundary", err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE recipes.recipes SET archived_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, rid); err != nil {
		t.Fatal("archive recipe fixture", err)
	}
	if _, err = f.store.RecipeInsights(f.ctx, f.identity, rid, "2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived recipe exposed through insights: %v", err)
	}
	if _, err = f.store.CurrentRecipeFingerprint(f.ctx, f.identity, rid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived recipe exposed through fingerprint: %v", err)
	}
	if _, err = f.store.SaveRecipeProfile(f.ctx, f.identity, rid, value.UUID(), value.UUID(),
		RecipeProfileInput{Generation: recipeGeneration, Version: Int(bundle.Recipe["version"]),
			Fingerprint: bundle.Fingerprint}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived recipe accepted profile update: %v", err)
	}
}

func recipeExpansionMigratePredecessor(t *testing.T, ctx context.Context, ownerURL string, owner *pgxpool.Pool) {
	t.Helper()
	// Create every allowed namespace before any migration inspects other schemas.
	for _, schema := range database.Schemas {
		if _, err := owner.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
			t.Fatal("create genuine predecessor schemas", err)
		}
	}
	// Freeze the historical release rather than upgrading and attempting to undo
	// later audit/readiness contracts before the legacy rows are populated.
	profile := map[string]uint{
		"core": 26, "shopping": 8, "fuel": 5, "recipes": 4, "pantry": 12,
		"expenses": 4, "chores": 1, "meters": 1, "documents": 1, "mealplan": 7,
	}
	for _, schema := range database.Schemas {
		version, ok := profile[schema]
		if !ok {
			t.Fatalf("unreviewed predecessor schema %s", schema)
		}
		recipeExpansionMigrateTo(t, ownerURL, schema, version)
	}
	if _, err := owner.Exec(ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal("install genuine predecessor audit triggers", err)
	}
}

func recipeExpansionMigrateTo(t *testing.T, ownerURL, schema string, version uint) {
	t.Helper()
	source, err := iofs.New(os.DirFS("../database"), "migrations/"+schema)
	if err != nil {
		t.Fatal("open predecessor migrations", err)
	}
	config, err := pgx.ParseConfig(ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		initialize, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(initialize, "SET search_path=pg_catalog"); err != nil {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_ = conn.Close(cleanup)
			return err
		}
		return nil
	}))
	defer func() { _ = db.Close() }()
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{
		SchemaName: schema, StatementTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal("open predecessor migration driver", err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		t.Fatal("open predecessor migration runner", err)
	}
	if err = runner.Migrate(version); err != nil {
		t.Fatal("migrate isolated schema to checked-in predecessor", err)
	}
	sourceErr, driverErr := runner.Close()
	if sourceErr != nil || driverErr != nil {
		t.Fatalf("close predecessor migration source/driver: %v/%v", sourceErr, driverErr)
	}
}

func TestRecipeExpansionPopulatedPredecessorUpgrade(t *testing.T) {
	f := newRecipeExpansionPredecessorFixture(t)
	// Populate the genuine predecessor columns and immutable history before the
	// first current migration, without downgrading a bootstrapped current schema.
	rid, ingredientID, batchID := value.UUID(), value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.recipes
 (id,household_id,title,base_servings,created_by)
 VALUES($1,$2,'Preserved predecessor recipe',2,$3)`, rid,
		f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal("seed predecessor recipe", err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO recipes.ingredients
 (id,household_id,recipe_id,position,name,quantity,unit_code,created_by)
 VALUES($1,$2,$3,1,'Old flour',100,'g',$4)`, ingredientID,
		f.identity.HouseholdID, rid, f.identity.UserID); err != nil {
		t.Fatal("seed predecessor ingredient", err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.import_batches
 (id,household_id,module_id,module_generation,entity,row_count,valid_count,error_count)
 VALUES($1,$2,'recipes',$3,'recipes',1,1,0)`, batchID,
		f.identity.HouseholdID, f.store.States()["recipes"].Generation); err != nil {
		t.Fatal("seed predecessor generic import", err)
	}
	var beforeVersions int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM recipes.versions
 WHERE household_id=$1 AND recipe_id=$2`, f.identity.HouseholdID, rid).
		Scan(&beforeVersions); err != nil || beforeVersions < 2 {
		t.Fatalf("predecessor immutable history missing: %d/%v", beforeVersions, err)
	}
	if err := database.Migrate(f.ctx, f.ownerURL); err != nil {
		t.Fatal("upgrade populated predecessor", err)
	}
	if err := database.Migrate(f.ctx, f.ownerURL); err != nil {
		t.Fatal("repeat upgraded migration", err)
	}
	if err := database.Ready(f.ctx, f.app); err != nil {
		t.Fatal("upgraded readiness", err)
	}
	var title, quantity, purpose string
	if err := f.owner.QueryRow(f.ctx, `SELECT r.title,i.quantity::text,b.purpose
 FROM recipes.recipes r JOIN recipes.ingredients i ON i.household_id=r.household_id
  AND i.recipe_id=r.id JOIN core.import_batches b ON b.household_id=r.household_id
 WHERE r.household_id=$1 AND r.id=$2 AND i.id=$3 AND b.id=$4`,
		f.identity.HouseholdID, rid, ingredientID, batchID).
		Scan(&title, &quantity, &purpose); err != nil || title != "Preserved predecessor recipe" ||
		quantity != "100.000000" || purpose != "flat_csv" {
		t.Fatalf("predecessor content/import changed: %q/%q/%q/%v", title, quantity, purpose, err)
	}
	var versions, invalidDigests int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER(WHERE
 snapshot_sha256<>sha256(convert_to(snapshot::text,'UTF8')))
 FROM recipes.versions WHERE household_id=$1 AND recipe_id=$2`, f.identity.HouseholdID, rid).
		Scan(&versions, &invalidDigests); err != nil || versions != beforeVersions || invalidDigests != 0 {
		t.Fatalf("immutable version digest backfill %d/%d/%d: %v",
			versions, beforeVersions, invalidDigests, err)
	}
	var immutableEnabled bool
	if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger
 WHERE tgrelid='recipes.versions'::regclass AND tgname='versions_immutable'
 AND tgenabled IN('O','A'))`).Scan(&immutableEnabled); err != nil || !immutableEnabled {
		t.Fatalf("version immutability not restored: %v/%v", immutableEnabled, err)
	}
	if _, err := f.app.Exec(f.ctx, `UPDATE recipes.versions SET snapshot='{}'::jsonb
 WHERE household_id=$1 AND recipe_id=$2`, f.identity.HouseholdID, rid); err == nil {
		t.Fatal("upgraded immutable recipe history accepted modification")
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE recipes.recipes SET title='After upgrade',
 version=version+1 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, rid); err != nil {
		t.Fatal("append post-upgrade recipe revision", err)
	}
	var lastRevision, lastDigestBytes int
	if err := f.owner.QueryRow(f.ctx, `SELECT revision,octet_length(snapshot_sha256)
 FROM recipes.versions WHERE household_id=$1 AND recipe_id=$2
 ORDER BY revision DESC LIMIT 1`, f.identity.HouseholdID, rid).
		Scan(&lastRevision, &lastDigestBytes); err != nil ||
		lastRevision != beforeVersions+1 || lastDigestBytes != sha256.Size {
		t.Fatalf("post-upgrade history digest %d/%d: %v", lastRevision, lastDigestBytes, err)
	}
}
