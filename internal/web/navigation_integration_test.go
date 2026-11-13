//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/module"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNavigationPreferencesIsolationAndValidation(t *testing.T) {
	f := newIntegrationFixture(t)
	type preferenceResult struct {
		Saved          bool     `json:"saved"`
		Theme          string   `json:"theme"`
		Locale         string   `json:"locale"`
		Offline        bool     `json:"offline"`
		Order          []string `json:"module_order"`
		EffectiveOrder []string `json:"effective_module_order"`
	}
	request := func(method, token, body string, status int) preferenceResult {
		t.Helper()
		r, raw, err := f.request(method, "/api/v1/core/preferences", token, f.app.Auth.CSRF(token, "session"),
			"application/json", strings.NewReader(body), nil)
		if err != nil || r.StatusCode != status {
			t.Fatalf("preferences: status=%v err=%v body=%s", r, err, raw)
		}
		var result preferenceResult
		if status == http.StatusOK {
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	defaultOrder := moduleIDs(f.app.Store.Modules)
	initial := request(http.MethodGet, f.token, "", 200)
	if initial.Order == nil || len(initial.Order) != 0 || !reflect.DeepEqual(initial.EffectiveOrder, defaultOrder) {
		t.Fatal("missing preferences must return an empty saved array and the complete default order", initial)
	}
	request(http.MethodPost, f.token, `{"theme":"pink-system","locale":"en","offline":true}`, 200)
	saved := request(http.MethodPost, f.token, `{"module_order":["documents","recipes","shopping"]}`, 200)
	if !saved.Saved || !reflect.DeepEqual(saved.Order, []string{"documents", "recipes", "shopping"}) || len(saved.EffectiveOrder) != len(defaultOrder) || saved.EffectiveOrder[0] != "documents" {
		t.Fatal("saved order or effective response is incomplete", saved)
	}
	got := request(http.MethodGet, f.token, "", 200)
	if got.Theme != "pink-system" || got.Locale != "en" || !got.Offline || !reflect.DeepEqual(got.Order, saved.Order) {
		t.Fatal("ordering changed unrelated preferences", got)
	}
	for _, body := range []string{
		`{"module_order":["recipes","recipes"]}`, `{"module_order":["unknown"]}`, `{"module_order":[""]}`,
		`{"module_order":["Fuel"]}`, `{"module_order":[null]}`, `{"module_order":null}`,
		`{"module_order":["shopping","fuel","recipes","pantry","expenses","chores","meters","documents","mealplan","fuel"]}`,
		`{"theme":"light","module_order":["unknown"]}`,
	} {
		request(http.MethodPost, f.token, body, 422)
		if after := request(http.MethodGet, f.token, "", 200); !reflect.DeepEqual(after, got) {
			t.Fatal("rejected preference write changed state", body, after)
		}
	}
	r, _, err := f.request(http.MethodPost, "/api/v1/core/preferences", f.token, "wrong", "application/json", strings.NewReader(`{"module_order":[]}`), nil)
	if err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatal("navigation mutation accepted invalid CSRF", r, err)
	}
	r, _, err = f.request(http.MethodPost, "/api/v1/core/preferences", "", "", "application/json", strings.NewReader(`{"module_order":[]}`), nil)
	if err != nil || r.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous navigation mutation accepted", r, err)
	}
	// Bootstrap and HTML use account ordering, while paused modules remain saved.
	r, raw, err := f.request(http.MethodGet, "/api/v1/core/bootstrap", f.token, "", "", nil, nil)
	var boot struct{ Modules []module.Descriptor }
	if err != nil || r.StatusCode != 200 || json.Unmarshal(raw, &boot) != nil || !reflect.DeepEqual(moduleIDs(boot.Modules), saved.EffectiveOrder) {
		t.Fatalf("bootstrap order differs: %v %v %s", r, err, raw)
	}
	req, err := f.app.accountLocale(httptest.NewRequestWithContext(f.ctx, http.MethodGet, "/settings", nil), f.identity)
	if err != nil || !reflect.DeepEqual(moduleIDs(f.app.page(req, "test").Modules), saved.EffectiveOrder) {
		t.Fatal("page order differs from account preference", err)
	}
	if !reflect.DeepEqual(moduleIDs(f.app.Store.Modules), defaultOrder) {
		t.Fatal("personal ordering mutated the shared registry")
	}
	r, raw, err = f.request(http.MethodGet, "/", f.token, "", "", nil, nil)
	if err != nil || r.StatusCode != http.StatusOK || !strings.Contains(string(raw), `id="today"`) || r.Header.Get("Location") != "" {
		t.Fatal("home did not render Today while retaining personal module ordering", r, err)
	}
	reader := shoppingTestMember(t, f, "navigationreader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.9", "navigation")
	if err != nil {
		t.Fatal(err)
	}
	if other := request(http.MethodGet, readerToken, "", 200); len(other.Order) != 0 || !reflect.DeepEqual(other.EffectiveOrder, defaultOrder) {
		t.Fatal("reader inherited another account's ordering", other)
	}
	request(http.MethodPost, readerToken, `{"module_order":["fuel"]}`, 200)
	if admin := request(http.MethodGet, f.token, "", 200); !reflect.DeepEqual(admin.Order, saved.Order) {
		t.Fatal("reader preference changed administrator ordering", admin)
	}
	// Partial concurrent writes update only the requested columns of the locked row.
	statuses := make(chan int, 2)
	for _, body := range []string{`{"theme":"dark"}`, `{"module_order":["fuel","recipes"]}`} {
		go func() {
			response, _, requestErr := f.request(http.MethodPost, "/api/v1/core/preferences", f.token,
				f.app.Auth.CSRF(f.token, "session"), "application/json", strings.NewReader(body), nil)
			if requestErr != nil {
				statuses <- 0
				return
			}
			statuses <- response.StatusCode
		}()
	}
	for range 2 {
		if status := <-statuses; status != 200 {
			t.Fatalf("concurrent partial preference write status %d", status)
		}
	}
	if concurrent := request(http.MethodGet, f.token, "", 200); concurrent.Theme != "dark" || !concurrent.Offline || concurrent.Locale != "en" || !reflect.DeepEqual(concurrent.Order, []string{"fuel", "recipes"}) {
		t.Fatal("concurrent partial updates overwrote independent preferences", concurrent)
	}
	var audited bool
	err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.audit_log WHERE actor_user_id=$1 AND resource_type='user_preferences'
 AND changes->'module_order'->'before'='["documents","recipes","shopping"]'::jsonb
 AND changes->'module_order'->'after'='["fuel","recipes"]'::jsonb
 AND changes->'module_order'->>'redacted'='false')`, f.identity.UserID).Scan(&audited)
	if err != nil || !audited {
		t.Fatal("exact navigation before/after values are not audited", err)
	}
	reset := request(http.MethodPost, f.token, `{"module_order":[]}`, 200)
	if reset.Order == nil || len(reset.Order) != 0 || !reflect.DeepEqual(reset.EffectiveOrder, defaultOrder) {
		t.Fatal("reset did not return default ordering", reset)
	}
	for _, query := range []string{
		"UPDATE core.user_preferences SET module_order=ARRAY['fuel','fuel'] WHERE user_id=$1",
		"UPDATE core.user_preferences SET module_order=ARRAY['unknown'] WHERE user_id=$1",
		"UPDATE core.user_preferences SET module_order=ARRAY['fuel',NULL] WHERE user_id=$1",
		"UPDATE core.user_preferences SET module_order=ARRAY[['fuel'],['recipes']] WHERE user_id=$1",
		"UPDATE core.user_preferences SET module_order='[0:0]={fuel}'::text[] WHERE user_id=$1",
	} {
		_, err := f.owner.Exec(f.ctx, query, f.identity.UserID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "user_preferences_module_order_check" {
			t.Fatalf("database did not reject invalid bounded order: %v", err)
		}
	}
	// The audit exception is limited to small arrays of approved module IDs.
	// Synthetic owner-only calls probe shapes the table constraint cannot store.
	for _, raw := range []string{`null`, `{}`, `"fuel"`, `["fuel","fuel"]`, `["unknown"]`, `[null]`, `[["fuel"]]`, `[1]`, `["` + strings.Repeat("x", 300) + `"]`} {
		_, err := f.owner.Exec(f.ctx, `SELECT core.write_audit_row('core','user_preferences','CREATE',NULL,
 jsonb_build_object('household_id',$1::text,'user_id',$2::text,'module_order',$3::jsonb))`,
			f.identity.HouseholdID, f.identity.UserID, raw)
		if err == nil || !strings.Contains(err.Error(), "invalid bounded navigation audit value") {
			t.Fatalf("audit accepted invalid order shape %s: %v", raw, err)
		}
	}
	_, err = f.owner.Exec(f.ctx, `SELECT core.write_audit_row('core','user_preferences','CREATE',NULL,
 jsonb_build_object('household_id',$1::text,'user_id',$2::text,'theme','[]'::jsonb))`,
		f.identity.HouseholdID, f.identity.UserID)
	if err == nil || !strings.Contains(err.Error(), "audit policy permits an unbounded structured value") {
		t.Fatal("navigation audit exception weakened other structured-value rejection", err)
	}
}

func restoreNavigationPredecessor(t *testing.T, f *integrationFixture) {
	t.Helper()
	restoreNotificationPredecessor(t, f)
	// Read the immutable migration used by the real runner, rather than infer a
	// predecessor from a possibly modified live security-definer function.
	migration, err := os.ReadFile("../database/migrations/core/000021_planning_purchases.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, writer, found := strings.Cut(string(migration), "CREATE OR REPLACE FUNCTION core.write_audit_row(")
	writer, _, closed := strings.Cut(writer, "END $$;")
	if !found || !closed {
		t.Fatal("reviewed audit writer migration is incomplete")
	}
	if _, err := f.owner.Exec(f.ctx, "CREATE OR REPLACE FUNCTION core.write_audit_row("+writer+"END $$;"); err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Exec(f.ctx, `ALTER TABLE core.user_preferences DROP COLUMN module_order;
DROP FUNCTION core.valid_module_order(text[]);
UPDATE core.audit_policies SET safe_columns=array_remove(safe_columns,'module_order') WHERE schema_name='core' AND table_name='user_preferences';
DO $restore$
DECLARE baseline jsonb;authority text;overview text;ready text;previous_authority text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 previous_authority=replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=36 AND NOT dirty)');
 EXECUTE previous_authority;
 EXECUTE replace(overview,'bool_and(version=33 AND NOT dirty)','bool_and(version=36 AND NOT dirty)');
 EXECUTE replace(ready,md5(authority),md5(previous_authority));
END $restore$;
UPDATE core.schema_migrations SET version=36 WHERE version=37 AND NOT dirty;`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNavigationPreferencesUpgradeFrom36(t *testing.T) {
	f := newIntegrationFixture(t)
	r, raw, err := f.request(http.MethodPost, "/api/v1/core/preferences", f.token, f.app.Auth.CSRF(f.token, "session"),
		"application/json", strings.NewReader(`{"theme":"pink-dark","locale":"en","offline":true}`), nil)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("existing preferences: %v %v %s", r, err, raw)
	}
	var before string
	if err := f.owner.QueryRow(f.ctx, "SELECT (to_jsonb(p)-'module_order')::text FROM core.user_preferences p WHERE user_id=$1", f.identity.UserID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	restoreNavigationPredecessor(t, f)
	var reviewed string
	if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('fuel.module_ready()'::regprocedure)").Scan(&reviewed); err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(reviewed, "AS $function$", "AS $function$\n-- unreviewed navigation predecessor\n", 1)
	if tampered == reviewed {
		t.Fatal("could not prepare unreviewed predecessor")
	}
	if _, err := f.owner.Exec(f.ctx, tampered); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil || !strings.Contains(err.Error(), "unreviewed Navigation preferences Fuel predecessor") {
		t.Fatalf("migration accepted unreviewed function: %v", err)
	}
	if _, err := f.owner.Exec(f.ctx, reviewed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=36,dirty=false"); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('core.write_audit_row(text,text,text,jsonb,jsonb)'::regprocedure)").Scan(&reviewed); err != nil {
		t.Fatal(err)
	}
	tampered = strings.Replace(reviewed, "AS $function$", "AS $function$\n-- unreviewed audit writer\n", 1)
	if tampered == reviewed {
		t.Fatal("could not prepare unreviewed audit writer")
	}
	if _, err := f.owner.Exec(f.ctx, tampered); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil || !strings.Contains(err.Error(), "unreviewed Navigation preferences audit writer predecessor") {
		t.Fatalf("migration accepted unreviewed audit writer: %v", err)
	}
	if _, err := f.owner.Exec(f.ctx, reviewed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=36,dirty=false"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("upgraded module and audit contracts not ready", err)
		}
		var after string
		var order []string
		if err := f.owner.QueryRow(f.ctx, "SELECT (to_jsonb(p)-'module_order')::text,module_order FROM core.user_preferences p WHERE user_id=$1", f.identity.UserID).Scan(&after, &order); err != nil || before != after || len(order) != 0 {
			t.Fatal("navigation migration changed existing preferences", err)
		}
	}
}
