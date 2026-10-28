//go:build integration

package web

import (
	"context"
	"net/url"
	"os"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func financialRequirementOwnerSQL(t *testing.T, f *integrationFixture, statement string, args ...any) error {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, statement, args...); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

func financialRequirementError(t *testing.T, err error, code string) {
	t.Helper()
	if pgerr, ok := err.(*pgconn.PgError); !ok || pgerr.Code != code {
		t.Fatal("unexpected financial requirement SQL result", code, err)
	}
}

func financialRequirementSet(t *testing.T, f *integrationFixture, entry store.Record, includeLine bool) string {
	t.Helper()
	set := value.UUID()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	count := 1
	if includeLine {
		count = 2
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code)
 VALUES($1,$2,'financial',$3,'financial_sources')`, set, f.identity.HouseholdID, count); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id)
 SELECT household_id,$1,id,policy_id FROM core.financial_subjects WHERE household_id=$2 AND policy_id=$3 AND ($4 OR resource_type='entries')`,
		set, f.identity.HouseholdID, financialPolicyID(t, f, entry), includeLine); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return set
}

func TestFinancialRequirementsRetainOriginalSubjectAndPolicyAfterSourceDeletion(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	set := financialRequirementSet(t, f, entry, true)
	var originalSubject, originalPolicy string
	if err := f.owner.QueryRow(f.ctx, `SELECT m.subject_id,m.policy_id FROM core.financial_requirement_members m
 JOIN core.financial_subjects s ON s.id=m.subject_id WHERE m.set_id=$1 AND s.resource_id=$2`, set, line["id"]).Scan(&originalSubject, &originalPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "DELETE FROM expenses.entry_lines WHERE id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	var retained bool
	if err := f.owner.QueryRow(f.ctx, `SELECT m.subject_id=$2 AND m.policy_id=$3 AND s.retired_at IS NOT NULL AND p.retired_at IS NULL
 FROM core.financial_requirement_members m JOIN core.financial_subjects s ON s.id=m.subject_id
 JOIN core.financial_policies p ON p.id=m.policy_id WHERE m.set_id=$1 AND m.subject_id=$2`, set, originalSubject, originalPolicy).Scan(&retained); err != nil || !retained {
		t.Fatal("deleted original line lost its separate denied identity while parent remained live", retained, err)
	}
}

func TestFinancialRequirementsExactCountAndOriginalPolicyPair(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,reason_code)
 VALUES($1,'financial',1,'financial_sources')`, f.identity.HouseholdID), "23514")
	set := financialRequirementSet(t, f, entry, false)
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id)
 SELECT household_id,$1,id,policy_id FROM core.financial_subjects WHERE resource_type='entry_lines' AND resource_id=$2`, set, line["id"]), "23514")
	other, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Separate original policy", "amount_minor": "1.00"})
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id)
 SELECT household_id,$1,id,$3 FROM core.financial_subjects WHERE resource_type='entry_lines' AND resource_id=$2`, set, line["id"], financialPolicyID(t, f, other)), "23503")
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,reason_code)
 VALUES($1,'unresolved',1025,'unresolved_legacy')`, f.identity.HouseholdID), "23514")
}

func TestFinancialRequirementsTypedKeysAndRevisionChain(t *testing.T) {
	f := newIntegrationFixture(t)
	set := value.UUID()
	if err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code)
 VALUES($1,$2,'nonfinancial',0,'nonfinancial_explicit')`, set, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, fields string }{
		{"missing bigint", "'audit',NULL,NULL,NULL"},
		{"audit is not UUID", "'audit',gen_random_uuid(),1,NULL"},
		{"nonpositive bigint", "'audit',NULL,0,NULL"},
		{"row requires ordinal", "'import_row',gen_random_uuid(),NULL,NULL"},
		{"row requires positive ordinal", "'import_row',gen_random_uuid(),NULL,0"},
		{"UUID key rejects ordinal", "'capture',gen_random_uuid(),NULL,1"},
		{"inherited result needs generation", "'financial_subject',gen_random_uuid(),NULL,NULL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// security: fields is a fixed test case, never request input.
			err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(household_id,set_id,revision,artifact_kind,object_id,audit_id,row_number)
 SELECT $1,$2,1,`+test.fields, f.identity.HouseholdID, set)
			financialRequirementError(t, err, "23514")
		})
	}
	object, first, second := value.UUID(), value.UUID(), value.UUID()
	if err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(id,household_id,set_id,revision,artifact_kind,object_id,created_at)
 VALUES($1,$2,$3,1,'attachment',$4,'2026-10-26')`, first, f.identity.HouseholdID, set, object); err != nil {
		t.Fatal(err)
	}
	if err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(id,household_id,set_id,revision,previous_id,artifact_kind,object_id,created_at)
 VALUES($1,$2,$3,2,$4,'attachment',$5,'2020-01-01')`, second, f.identity.HouseholdID, set, first, object); err != nil {
		t.Fatal(err)
	}
	var latest string
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM core.financial_artifact_scope_versions WHERE household_id=$1 AND artifact_kind='attachment' AND object_id=$2 ORDER BY revision DESC LIMIT 1`, f.identity.HouseholdID, object).Scan(&latest); err != nil || latest != second {
		t.Fatal("scope head followed timestamp rather than revision", latest, err)
	}
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(household_id,set_id,revision,previous_id,artifact_kind,object_id)
 VALUES($1,$2,3,$3,'attachment',$4)`, f.identity.HouseholdID, set, first, object), "23514")
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(household_id,set_id,revision,previous_id,artifact_kind,object_id)
 VALUES($1,$2,2,$3,'attachment',$4)`, f.identity.HouseholdID, set, first, value.UUID()), "23514")
	for _, key := range []struct {
		kind   string
		object any
		audit  any
		row    any
	}{
		{"audit", nil, int64(9007199254740993), nil}, {"import_row", value.UUID(), nil, 501},
	} {
		if err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(household_id,set_id,revision,artifact_kind,object_id,audit_id,row_number)
 VALUES($1,$2,1,$3,$4,$5,$6)`, f.identity.HouseholdID, set, key.kind, key.object, key.audit, key.row); err != nil {
			t.Fatal("exact bigint or composite CSV source key was lost", key, err)
		}
	}
}

func TestFinancialRequirementsAppCannotWriteEvenInsideOwnerDefiner(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, table := range []string{"financial_requirement_sets", "financial_requirement_members", "financial_artifact_scope_versions"} {
		for _, operation := range []string{"INSERT INTO core." + table + " SELECT * FROM core." + table + " WHERE false", "UPDATE core." + table + " SET household_id=household_id", "DELETE FROM core." + table, "TRUNCATE core." + table} {
			_, err := f.pool.Exec(f.ctx, operation)
			financialRequirementError(t, err, "42501")
		}
	}
	// Only this disposable test probe is owner-definer. The App may call it,
	// but the infrastructure trigger still sees the real App session and denies.
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.test_requirement_definer(uuid) RETURNS void LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $probe$
 INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,reason_code) VALUES($1,'nonfinancial',0,'nonfinancial_explicit') $probe$;
 REVOKE ALL ON FUNCTION core.test_requirement_definer(uuid) FROM PUBLIC; GRANT EXECUTE ON FUNCTION core.test_requirement_definer(uuid) TO household_app;`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.owner.Exec(context.Background(), "DROP FUNCTION IF EXISTS core.test_requirement_definer(uuid)")
	})
	_, err := f.pool.Exec(f.ctx, "SELECT core.test_requirement_definer($1)", f.identity.HouseholdID)
	financialRequirementError(t, err, "42501")
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.financial_requirement_sets").Scan(&count); err != nil || count != 0 {
		t.Fatal("definer frame bypassed maintenance distinction", count, err)
	}
}

func TestFinancialRequirementsOwnerRolePreservesIdentityAndReadiness(t *testing.T) {
	f := newIntegrationFixture(t)
	set := value.UUID()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "SET LOCAL ROLE household_owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code,created_at)
 VALUES($1,$2,'unresolved',0,'unresolved_missing_origin','2001-02-03T04:05:06Z')`, set, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err = f.pool.QueryRow(f.ctx, "SELECT id=$1 AND created_at='2001-02-03T04:05:06Z' AND classification='unresolved' FROM core.financial_requirement_sets WHERE id=$1", set).Scan(&preserved); err != nil || !preserved {
		t.Fatal("owner role lost exact restored identity/classification/time", preserved, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"financial_requirement_sets", "financial_requirement_members", "financial_artifact_scope_versions"} {
		for _, privilege := range []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE", "TRIGGER", "REFERENCES"} {
			if _, err = f.owner.Exec(f.ctx, "GRANT "+privilege+" ON core."+table+" TO household_app"); err != nil {
				t.Fatal(err)
			}
			ready := database.Ready(f.ctx, f.pool)
			if _, err = f.owner.Exec(f.ctx, "REVOKE "+privilege+" ON core."+table+" FROM household_app"); err != nil {
				t.Fatal(err)
			}
			if ready == nil {
				t.Fatal("unexpected producer privilege accepted", table, privilege)
			}
		}
		if _, err = f.owner.Exec(f.ctx, "REVOKE SELECT ON core."+table+" FROM household_app"); err != nil {
			t.Fatal(err)
		}
		ready := database.Ready(f.ctx, f.pool)
		if _, err = f.owner.Exec(f.ctx, "GRANT SELECT ON core."+table+" TO household_app"); err != nil {
			t.Fatal(err)
		}
		if ready == nil {
			t.Fatal("missing inspection privilege accepted", table)
		}
		for _, trigger := range []string{"financial_infrastructure_immutable", "financial_infrastructure_truncate", "financial_infrastructure_complete"} {
			if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core."+table+" DISABLE TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
			ready = database.Ready(f.ctx, f.pool)
			if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core."+table+" ENABLE TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
			if ready == nil {
				t.Fatal("missing infrastructure guard accepted", table, trigger)
			}
		}
	}
	var policies int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_policies").Scan(&policies); err != nil || policies != 89 {
		t.Fatal("security infrastructure recursively joined row audit", policies, err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.financial_artifact_scope_versions ALTER COLUMN object_id TYPE text USING object_id::text"); err != nil {
		t.Fatal(err)
	}
	ready := database.Ready(f.ctx, f.pool)
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.financial_artifact_scope_versions ALTER COLUMN object_id TYPE uuid USING object_id::uuid"); err != nil {
		t.Fatal(err)
	}
	if ready == nil {
		t.Fatal("non-UUID artifact key type was accepted")
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("restored typed key did not restore readiness", err)
	}
}

func TestFinancialRequirementsArtifactBoundsAndPrivateImportClass(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	for _, test := range []struct {
		name, kind string
		count      int
	}{
		{"attachment16 accepted", "attachment", 16},
		{"capture17 rejected", "capture", 17},
		{"import1000 accepted", "import_batch", 1000},
		{"import1001 rejected", "import_batch", 1001},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(f.ctx) }()
			set := value.UUID()
			if _, err = tx.Exec(f.ctx, `INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code)
 VALUES($1,$2,'financial',$3,'financial_sources')`, set, f.identity.HouseholdID, test.count); err != nil {
				t.Fatal(err)
			}
			// Owner synthetic retained identities model deleted historical lines.
			// No optional-schema records or replacement policies are invented.
			if _, err = tx.Exec(f.ctx, `WITH subjects AS(INSERT INTO core.financial_subjects(household_id,module_id,module_generation,resource_type,resource_id,policy_id,retired_at,created_by,changed_by)
 SELECT $1,'expenses',$2,'entry_lines',gen_random_uuid(),$3,clock_timestamp(),$4,$4 FROM generate_series(1,$5::integer) RETURNING household_id,id,policy_id)
 INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id) SELECT household_id,$6,id,policy_id FROM subjects`,
				f.identity.HouseholdID, f.app.Store.States()["expenses"].Generation, financialPolicyID(t, f, entry), f.identity.UserID, test.count, set); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(f.ctx, `INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id)
 VALUES($1,$2,$3,1,$4)`, f.identity.HouseholdID, test.kind, value.UUID(), set); err != nil {
				t.Fatal(err)
			}
			err = tx.Commit(f.ctx)
			if test.count == 17 || test.count == 1001 {
				financialRequirementError(t, err, "23514")
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	set := value.UUID()
	if err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,creator_user_id,reason_code)
 VALUES($1,$2,'private_import',0,$3,'private_import_preview')`, set, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	financialRequirementError(t, financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id)
 VALUES($1,'attachment',$2,1,$3)`, f.identity.HouseholdID, value.UUID(), set), "23514")
}

func TestFinancialRequirementsPopulatedCore25UpgradeAndRepeatPreserveFacts(t *testing.T) {
	f := financialPriorFixture(t, true)
	migratePriorAuditSchema(t, f, "core", 25)
	migratePriorAuditSchema(t, f, "expenses", 4)
	before := financialDomainSnapshot(t, f)
	var oldIdentities string
	const identities = `SELECT jsonb_build_object('policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM core.financial_policies p),
 'subjects',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM core.financial_subjects s))::text`
	if err := f.owner.QueryRow(f.ctx, identities).Scan(&oldIdentities); err != nil {
		t.Fatal(err)
	}
	ownerURL, err := url.Parse(os.Getenv("HOUSEHOLD_TEST_OWNER_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := ownerURL.Query()
	query.Set("search_path", "core,pg_catalog")
	ownerURL.RawQuery = query.Encode()
	for range 2 {
		if err := database.Migrate(f.ctx, ownerURL.String()); err != nil {
			t.Fatal(err)
		}
		var afterIdentities string
		if err := f.owner.QueryRow(f.ctx, identities).Scan(&afterIdentities); err != nil || afterIdentities != oldIdentities || financialDomainSnapshot(t, f) != before {
			t.Fatal("Core26 changed original facts or retained identity", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal(err)
		}
	}
	var inferred int
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.financial_requirement_sets)+(SELECT count(*) FROM core.financial_requirement_members)+(SELECT count(*) FROM core.financial_artifact_scope_versions)`).Scan(&inferred); err != nil || inferred != 0 {
		t.Fatal("foundation inferred historical provenance", inferred, err)
	}
}

func TestFinancialRequirementsInheritedResultScopeUsesCoreIdentityAndExcludesOwnAnchor(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	set := financialRequirementSet(t, f, entry, false)
	other, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Result primary identity", "amount_minor": "1.00"})
	var original, result string
	if err := f.owner.QueryRow(f.ctx, "SELECT id FROM core.financial_subjects WHERE resource_type='entries' AND resource_id=$1", entry["id"]).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, "SELECT id FROM core.financial_subjects WHERE resource_type='entries' AND resource_id=$1", other["id"]).Scan(&result); err != nil {
		t.Fatal(err)
	}
	generation := f.app.Store.States()["expenses"].Generation
	for _, test := range []struct {
		name, subject string
		generation    int64
		accepted      bool
	}{
		{"own anchor", original, generation, false},
		{"unknown Core identity", value.UUID(), generation, false},
		{"wrong source generation", result, generation + 1, false},
		{"separate primary plus inherited original", result, generation, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := financialRequirementOwnerSQL(t, f, `INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id,source_module,source_generation)
 VALUES($1,'financial_subject',$2,1,$3,'expenses',$4)`, f.identity.HouseholdID, test.subject, set, test.generation)
			if test.accepted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				financialRequirementError(t, err, "23514")
			}
		})
	}
}
