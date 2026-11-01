//go:build integration

package web

import (
	"context"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func financialPolicyID(t *testing.T, f *integrationFixture, entry store.Record) string {
	t.Helper()
	var policy string
	if err := f.owner.QueryRow(f.ctx, `SELECT id FROM core.financial_policies
	 WHERE household_id=$1 AND module_id='expenses' AND module_generation=$2 AND resource_type='entries' AND resource_id=$3`,
		f.identity.HouseholdID, f.app.Store.States()["expenses"].Generation, entry["id"]).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestFinancialFoundationBindsSharedEntryAndCompleteChildren(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	policy := financialPolicyID(t, f, entry)
	var shared, unknown, live, same bool
	var subjects int
	if err := f.owner.QueryRow(f.ctx, `SELECT p.access_mode='household',p.responsible_membership_id IS NULL,
	 p.retired_at IS NULL,count(s.id),bool_and(s.policy_id=p.id AND s.retired_at IS NULL)
	 FROM core.financial_policies p JOIN core.financial_subjects s ON s.household_id=p.household_id AND s.policy_id=p.id
	 WHERE p.id=$1 GROUP BY p.id`, policy).Scan(&shared, &unknown, &live, &subjects, &same); err != nil || !shared || !unknown || !live || !same || subjects != 2 {
		t.Fatal("new shared entry/line lack complete explicit policy identity", shared, unknown, live, subjects, same, err)
	}
	var linePolicy string
	if err := f.pool.QueryRow(f.ctx, "SELECT policy_id FROM core.financial_subjects WHERE household_id=$1 AND resource_type='entry_lines' AND resource_id=$2", f.identity.HouseholdID, line["id"]).Scan(&linePolicy); err != nil || linePolicy != policy {
		t.Fatal("child does not inherit parent policy", linePolicy, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestFinancialFoundationRolloutBarrierAndIdentityGuards(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	policy := financialPolicyID(t, f, entry)
	for _, test := range []struct{ statement, code string }{
		{"UPDATE core.financial_policies SET access_mode='selected' WHERE id=$1", "42501"},
		{"UPDATE core.financial_policies SET module_generation=module_generation+1 WHERE id=$1", "42501"},
		{"UPDATE core.financial_policies SET resource_id=gen_random_uuid() WHERE id=$1", "42501"},
	} {
		err := expenseIntegrityAppSQL(t, f, test.statement, policy)
		if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != test.code {
			t.Fatal("rollout or immutable identity accepted", test.statement, err)
		}
	}
	other, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Other shared identity", "amount_minor": "1.00"})
	otherPolicy := financialPolicyID(t, f, other)
	if err := expenseIntegrityAppSQL(t, f, "UPDATE core.financial_subjects SET policy_id=$1 WHERE resource_type='entry_lines' AND resource_id=$2", otherPolicy, line["id"]); err == nil {
		t.Fatal("existing child policy was rebound")
	}
	for _, table := range []string{"financial_policies", "financial_subjects"} {
		err := expenseIntegrityAppSQL(t, f, "DELETE FROM core."+table+" WHERE household_id=$1", f.identity.HouseholdID)
		if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != "42501" {
			t.Fatal("runtime role can delete financial history", table, err)
		}
	}
	var originalVersion int64
	if err := f.pool.QueryRow(f.ctx, "SELECT version FROM core.financial_policies WHERE id=$1", policy).Scan(&originalVersion); err != nil {
		t.Fatal(err)
	}
	if err := expenseIntegrityAppSQL(t, f, "UPDATE core.financial_policies SET version=999,updated_at='1970-01-01' WHERE id=$1", policy); err == nil {
		t.Fatal("client-controlled metadata bypassed the private identity factory")
	}
	var honest bool
	if err := f.pool.QueryRow(f.ctx, "SELECT version=$3 AND updated_at>'2026-01-01' AND changed_by=$2 FROM core.financial_policies WHERE id=$1", policy, f.identity.UserID, originalVersion).Scan(&honest); err != nil || !honest {
		t.Fatal("server revision/time/actor were client-controlled", honest, err)
	}
}

func TestFinancialFoundationDeletionRetainsDeniedIdentity(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	policy := financialPolicyID(t, f, entry)
	current, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(entry["id"]), current.Header.Version,
		f.app.Store.States()["expenses"].Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var retired bool
	var subjects, sources int
	if err := f.pool.QueryRow(f.ctx, `SELECT p.retired_at IS NOT NULL AND bool_and(s.retired_at IS NOT NULL),count(s.id),
	 (SELECT count(*) FROM expenses.entries WHERE id=$2)+(SELECT count(*) FROM expenses.entry_lines WHERE id=$3)
	 FROM core.financial_policies p JOIN core.financial_subjects s ON s.household_id=p.household_id AND s.policy_id=p.id
	 WHERE p.id=$1 GROUP BY p.id`, policy, entry["id"], line["id"]).Scan(&retired, &subjects, &sources); err != nil || !retired || subjects != 2 || sources != 0 {
		t.Fatal("cascade lost historical identity or retained live access", retired, subjects, sources, err)
	}
	if err := expenseIntegrityAppSQL(t, f, "UPDATE core.financial_policies SET retired_at=NULL WHERE id=$1", policy); err == nil {
		t.Fatal("retired identity was resurrected")
	}
}

func TestFinancialFoundationRejectsPhantomsAndPrematureRetirement(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	policy := financialPolicyID(t, f, entry)
	for _, target := range []struct{ table, column, id string }{
		{"financial_policies", "id", policy},
		{"financial_subjects", "resource_id", store.String(entry["id"])},
		{"financial_subjects", "resource_id", store.String(line["id"])},
	} {
		err := expenseIntegrityAppSQL(t, f, "UPDATE core."+target.table+" SET retired_at=clock_timestamp() WHERE "+target.column+"=$1", target.id)
		if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != "42501" {
			t.Fatal("live source identity retired before deletion", target.table, err)
		}
	}
	err := expenseIntegrityAppSQL(t, f, `INSERT INTO core.financial_subjects(household_id,module_id,module_generation,resource_type,resource_id,policy_id,created_by,changed_by)
	 VALUES($1,'expenses',$2,'entry_lines',$3,$4,$5,$5)`, f.identity.HouseholdID, f.app.Store.States()["expenses"].Generation, value.UUID(), policy, f.identity.UserID)
	if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != "42501" {
		t.Fatal("phantom source binding committed", err)
	}
	var policies, subjects int
	var live bool
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.financial_policies),count(*),bool_and(retired_at IS NULL)
	 FROM core.financial_subjects`).Scan(&policies, &subjects, &live); err != nil || policies != 1 || subjects != 2 || !live {
		t.Fatal("rejected metadata mutations left partial state", policies, subjects, live, err)
	}
}

func TestFinancialFoundationReadinessRejectsPrivilegesAndRemovedBarrier(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	policy := financialPolicyID(t, f, entry)
	for _, table := range []string{"financial_policies", "financial_subjects"} {
		for _, permission := range []string{"SELECT", "INSERT", "UPDATE"} {
			if _, err := f.owner.Exec(f.ctx, "REVOKE "+permission+" ON core."+table+" FROM household_app"); err != nil {
				t.Fatal(err)
			}
			err := database.Ready(f.ctx, f.pool)
			if _, restore := f.owner.Exec(f.ctx, "GRANT "+permission+" ON core."+table+" TO household_app"); restore != nil {
				t.Fatal(restore)
			}
			if err == nil {
				t.Fatal("missing individual financial privilege accepted", table, permission)
			}
		}
	}
	for _, target := range []struct{ table, trigger string }{
		{"core.financial_policies", "financial_policy_identity"},
		{"core.financial_policies", "financial_policy_complete"},
		{"core.financial_subjects", "financial_subject_identity"},
		{"core.financial_subjects", "financial_subject_complete"},
		{"expenses.entries", "financial_subject"},
		{"expenses.entry_lines", "financial_subject"},
	} {
		if _, err := f.owner.Exec(f.ctx, "ALTER TABLE "+target.table+" DISABLE TRIGGER "+target.trigger); err != nil {
			t.Fatal(err)
		}
		err := database.Ready(f.ctx, f.pool)
		if _, restore := f.owner.Exec(f.ctx, "ALTER TABLE "+target.table+" ENABLE TRIGGER "+target.trigger); restore != nil {
			t.Fatal(restore)
		}
		if err == nil {
			t.Fatal("disabled financial trigger accepted", target.table, target.trigger)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "ALTER TABLE core.financial_policies DROP CONSTRAINT financial_access_mode"); err != nil {
		t.Fatal(err)
	}
	err := database.Ready(f.ctx, f.pool)
	if _, restore := f.owner.Exec(f.ctx, "ALTER TABLE core.financial_policies ADD CONSTRAINT financial_access_mode CHECK(access_mode IN('household','selected'))"); restore != nil {
		t.Fatal(restore)
	}
	if err == nil {
		t.Fatal("missing current access-mode constraint accepted")
	}
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	if _, err := audit.Exec(ctx, f.pool, "UPDATE core.financial_policies SET access_mode='selected' WHERE id=$1", policy); err == nil {
		t.Fatal("direct selected-access mutation bypassed its private factory")
	}
}

func TestFinancialFoundationSourceContentionFailsBeforeRelease(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	current, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	input := auditMutation(t, f, "expenses", store.Record{"description": "Source-lock retry"}, current.Header.Version)
	lock, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(f.ctx) }()
	if _, err := lock.Exec(f.ctx, "SELECT id FROM expenses.entries WHERE id=$1 FOR UPDATE", entry["id"]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
	defer cancel()
	_, err = f.app.Store.Write(ctx, f.identity, m, e, store.String(entry["id"]), value.UUID(), value.UUID(), input)
	if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != "55P03" {
		t.Fatal("policy/source opposite order waited or bypassed the source lock", err)
	}
	if err := lock.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, e, store.String(entry["id"]), value.UUID(), value.UUID(), input); err != nil {
		t.Fatal("released source could not be retried", err)
	}
}

func financialPriorFixture(t *testing.T, withGeneration bool) *integrationFixture {
	t.Helper()
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "chores" {
			version = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		if schema == "mealplan" {
			version = 7 // Mealplan8 belongs to the later Core28 extension.
		}
		if schema == "recipes" {
			version = 4 // Recipes5 belongs to the later Core27 expansion.
		}
		if schema == "fuel" {
			version = 2 // Fuel5 requires Core26; this fixture begins at Core24.
		}
		if schema == "shopping" {
			version = 7 // Shopping8 likewise requires Core26.
		}
		if schema == "core" {
			version = 24
		}
		if schema == "pantry" {
			version = 11 // Preserve the genuine pre-Core26/Fuel5 extension predecessor.
		}
		if schema == "expenses" {
			version = 3
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hh, user, category, posted, legacy := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	seed, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seed.Rollback(f.ctx) }()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior financial policy fixture')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-financial-policy','Historical financial author','fixture-no-login')", []any{user}},
		{"INSERT INTO expenses.categories(id,household_id,name,created_by) VALUES($1,$2,'Original category',$3)", []any{category, hh, user}},
		{`INSERT INTO expenses.entries(id,household_id,occurred_on,description,payee,amount_minor,kind,state,posted_at,created_by,version)
	 VALUES($1,$3,'2026-10-26','Preserved signed posted','Original payee',999999999999,'expense','posted',clock_timestamp(),$4,17),
	 ($2,$3,'2026-10-26','Preserved101','',101,'expense','draft',NULL,$4,23)`, []any{posted, legacy, hh, user}},
		{`INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by,version)
	 VALUES($1,$2,$3,1000000000000,'Original amount',0,$4,3),($1,$2,$3,-1,'Original discount',1,$4,4)`, []any{hh, posted, category, user}},
		{`INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
	 SELECT $1,$2,$3,1,'Original legacy line '||n,n,$4 FROM generate_series(0,100)n`, []any{hh, legacy, category, user}},
	} {
		if _, err := seed.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if withGeneration {
		if _, err := seed.Exec(f.ctx, `WITH installation AS(INSERT INTO core.module_installations(module_id,schema_name,last_seen_build)
	 VALUES('expenses','expenses','historical-fixture') RETURNING module_id)
	 INSERT INTO core.module_states(household_id,module_id,state,data_generation) SELECT $1,module_id,'disabled',9 FROM installation`, hh); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func financialDomainSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var snapshot string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
	 'entries',(SELECT jsonb_agg((to_jsonb(e)-'money_mode') ORDER BY id) FROM expenses.entries e),
	 'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM expenses.entry_lines l),
	 'categories',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM expenses.categories c))::text`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestFinancialFoundationPopulatedPriorUpgradePreservesExactLegacy(t *testing.T) {
	f := financialPriorFixture(t, true)
	before := financialDomainSnapshot(t, f)
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if after := financialDomainSnapshot(t, f); before != after {
		t.Fatal("financial identity backfill changed original facts or content versions")
	}
	assertLegacyFinanceMoneyMode(t, f)
	var policies, subjects int
	var explicit bool
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.financial_policies),count(*),bool_and(
	 p.module_generation=9 AND s.module_generation=9 AND p.responsible_membership_id IS NULL AND p.access_mode='household'
	 AND p.retired_at IS NULL AND s.retired_at IS NULL)
	 FROM core.financial_subjects s JOIN core.financial_policies p ON p.household_id=s.household_id AND p.id=s.policy_id`).Scan(&policies, &subjects, &explicit); err != nil || policies != 2 || subjects != 105 || !explicit {
		t.Fatal("backfill lost exact generation, unknown responsibility or legacy101 completeness", policies, subjects, explicit, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestFinancialFoundationMissingGenerationAbortsWithoutPartialBindings(t *testing.T) {
	f := financialPriorFixture(t, false)
	before := financialDomainSnapshot(t, f)
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil {
		t.Fatal("missing historical generation was invented")
	}
	var policies, subjects int
	if err := f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.financial_policies),(SELECT count(*) FROM core.financial_subjects)").Scan(&policies, &subjects); err != nil || policies != 0 || subjects != 0 {
		t.Fatal("failed migration left partial financial bindings", policies, subjects, err)
	}
	if after := financialDomainSnapshot(t, f); before != after {
		t.Fatal("failed binding migration changed original financial facts")
	}
}

func TestFinancialFoundationActualPurgeRetiresOldGenerationWithoutRebinding(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	policy := financialPolicyID(t, f, entry)
	state := f.app.Store.States()["expenses"]
	const identitySQL = `SELECT jsonb_build_object('policies',(SELECT jsonb_agg(jsonb_build_array(p.id,p.resource_id,p.module_generation) ORDER BY p.id)
	 FROM core.financial_policies p WHERE p.household_id=$1 AND p.module_generation=$2),
	 'subjects',(SELECT jsonb_agg(jsonb_build_array(s.id,s.resource_type,s.resource_id,s.policy_id,s.module_generation) ORDER BY s.id)
	 FROM core.financial_subjects s WHERE s.household_id=$1 AND s.module_generation=$2))::text`
	var before, after string
	if err := f.owner.QueryRow(f.ctx, identitySQL, f.identity.HouseholdID, state.Generation).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "expenses", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, purge := value.UUID(), value.UUID()
	// Synthetic metadata authorizes actual isolated purge, not a backup rehearsal.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
	 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'fixture','isolated-financial-test',$2)`, backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
	 VALUES($1,$2,'expenses',$3,$4,$5)`, purge, f.identity.HouseholdID, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	next := f.app.Store.States()["expenses"]
	if next.Generation != state.Generation+1 {
		t.Fatal("purge failed to advance generation", next)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "expenses", true, next.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	newEntry, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "New generation shared entry", "amount_minor": "1.00"})
	newPolicy := financialPolicyID(t, f, newEntry)
	if newPolicy == policy {
		t.Fatal("purged policy identity was reused")
	}
	if err := f.owner.QueryRow(f.ctx, identitySQL, f.identity.HouseholdID, state.Generation).Scan(&after); err != nil || before != after {
		t.Fatal("old identity was deleted or rebound across generation", err)
	}
	var retired bool
	if err := f.owner.QueryRow(f.ctx, `SELECT p.retired_at IS NOT NULL AND bool_and(s.retired_at IS NOT NULL)
	 FROM core.financial_policies p JOIN core.financial_subjects s ON s.household_id=p.household_id AND s.policy_id=p.id
	 WHERE p.id=$1 GROUP BY p.id`, policy).Scan(&retired); err != nil || !retired {
		t.Fatal("purged financial history remains live", retired, err)
	}
}
