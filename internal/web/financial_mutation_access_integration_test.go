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
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func financialMutationSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var extra string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'generic',(SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY household_id,user_id,operation,key),'[]') FROM core.idempotency_records i),
 'links',(SELECT coalesce(jsonb_agg(to_jsonb(l) ORDER BY id),'[]') FROM core.resource_links l))::text`).Scan(&extra); err != nil {
		t.Fatal(err)
	}
	return financialEditorAccessSnapshot(t, f) + "\n" + extra
}

func TestFinancialMutationGenericReplayRequiresLiveCreatedEntryAndChild(t *testing.T) {
	for _, entity := range []string{"entries", "entry_lines"} {
		t.Run(entity, func(t *testing.T) {
			f, category, parent, _ := expenseIntegrityFixture(t)
			m, _ := f.app.Store.Module("expenses")
			e, _ := m.Entity(entity)
			data := store.Record{"occurred_on": "2026-10-26", "description": "Generic live replay", "amount_minor": "2.00"}
			if entity == "entry_lines" {
				data = store.Record{"entry_id": parent["id"], "category_id": category["id"], "amount_minor": "1.00", "note": "Generic child replay", "position": 1}
			}
			in := auditMutation(t, f, "expenses", data, 0)
			key := value.UUID()
			first, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", key, value.UUID(), in)
			if err != nil {
				t.Fatal(err)
			}
			before := financialMutationSnapshot(t, f)
			if got, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", key, value.UUID(), in); err != nil || store.String(got["id"]) != store.String(first["id"]) || financialMutationSnapshot(t, f) != before {
				t.Fatal("current generic replay lost its identity or repeated effects", got, err)
			}
			if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(first["id"]), store.Int(first["version"]), in.Generation, value.UUID()); err != nil {
				t.Fatal(err)
			}
			before = financialMutationSnapshot(t, f)
			if got, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", key, value.UUID(), in); !errors.Is(err, store.ErrNotFound) || got != nil || financialMutationSnapshot(t, f) != before {
				t.Fatal("deleted generic result escaped through a stored response", got, err)
			}
		})
	}
}

func TestFinancialMutationUppercaseExistingIdentityReplaysWithoutChangingNamespace(t *testing.T) {
	for _, entity := range []string{"entries", "entry_lines"} {
		t.Run(entity, func(t *testing.T) {
			f, _, entry, line := expenseIntegrityFixture(t)
			m, _ := f.app.Store.Module("expenses")
			e, _ := m.Entity(entity)
			resource := store.String(entry["id"])
			data := store.Record{"description": "Uppercase entry edit"}
			if entity == "entry_lines" {
				resource = store.String(line["id"])
				data = store.Record{"note": "Uppercase child edit"}
			}
			current, err := f.app.Store.Get(f.ctx, f.identity, m, e, resource)
			if err != nil {
				t.Fatal(err)
			}
			in := auditMutation(t, f, "expenses", data, store.Int(current["version"]))
			key, pathID := value.UUID(), strings.ToUpper(resource)
			first, err := f.app.Store.Write(f.ctx, f.identity, m, e, pathID, key, value.UUID(), in)
			if err != nil {
				t.Fatal(err)
			}
			before := financialMutationSnapshot(t, f)
			got, err := f.app.Store.Write(f.ctx, f.identity, m, e, pathID, key, value.UUID(), in)
			if err != nil || store.String(got["id"]) != store.String(first["id"]) || financialMutationSnapshot(t, f) != before {
				t.Fatal("uppercase identity rejected an identical authorized retry", got, err)
			}
			var operation string
			if err = f.pool.QueryRow(f.ctx, "SELECT operation FROM core.idempotency_records WHERE key=$1", key).Scan(&operation); err != nil || operation != "write/expenses/"+entity+"/"+pathID {
				t.Fatal("identity comparison changed historical operation keys", operation, err)
			}
		})
	}
}

func TestFinancialMutationOldParentCannotBeBypassedByPermittedNewParent(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	other, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Permitted new parent", "amount_minor": "1.00"})
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entry_lines")
	before := financialMutationSnapshot(t, f)
	in := auditMutation(t, f, "expenses", store.Record{"entry_id": other["id"], "note": "Must not move hidden allocation"}, store.Int(line["version"]))
	if got, err := f.app.Store.Write(f.ctx, f.identity, m, e, store.String(line["id"]), value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrNotFound) || got != nil || financialMutationSnapshot(t, f) != before {
		t.Fatal("supplied new parent bypassed actual stored source", got, err)
	}
}

func TestFinancialMutationBrokenBindingDeniesPostDeleteAndAttachmentLock(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	current, err := f.app.Store.Get(f.ctx, f.identity, m, e, store.String(entry["id"]))
	if err != nil {
		t.Fatal(err)
	}
	in := auditMutation(t, f, "expenses", store.Record{}, store.Int(current["version"]))
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.finance_origin_resources WHERE resource_type='entry_lines' AND resource_id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	before := financialMutationSnapshot(t, f)
	if got, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(entry["id"]), "post", value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrNotFound) || got != nil {
		t.Fatal("posting read or changed an incomplete financial source", got, err)
	}
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(entry["id"]), in.Version, in.Generation, value.UUID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("delete bypassed missing financial binding", err)
	}
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "expenses", in.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if err = f.app.Store.LockResource(f.ctx, tx, f.identity, "expenses", "entries", store.String(entry["id"])); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("attachment/capture edit lock bypassed incomplete source", err)
	}
	if financialMutationSnapshot(t, f) != before {
		t.Fatal("denied financial operations left effects")
	}
}

func TestFinancialMutationParentAndChildContentionFailBeforeRelease(t *testing.T) {
	for _, target := range []string{"entries", "entry_lines"} {
		t.Run(target, func(t *testing.T) {
			f, _, entry, line := expenseIntegrityFixture(t)
			held, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = held.Rollback(f.ctx) }()
			resource := entry["id"]
			if target == "entry_lines" {
				resource = line["id"]
			}
			// security: target is the two fixed table choices above.
			if _, err = held.Exec(f.ctx, "SELECT id FROM expenses."+target+" WHERE id=$1 FOR UPDATE", resource); err != nil {
				t.Fatal(err)
			}
			m, _ := f.app.Store.Module("expenses")
			e, _ := m.Entity("entry_lines")
			in := auditMutation(t, f, "expenses", store.Record{"note": "Retry after source contention"}, store.Int(line["version"]))
			key := value.UUID()
			before := financialMutationSnapshot(t, f)
			ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
			defer cancel()
			got, err := f.app.Store.Write(ctx, f.identity, m, e, store.String(line["id"]), key, value.UUID(), in)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "55P03" || got != nil || financialMutationSnapshot(t, f) != before {
				t.Fatal("opposite source lock waited or left effects", got, err)
			}
			if err = held.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = f.app.Store.Write(f.ctx, f.identity, m, e, store.String(line["id"]), key, value.UUID(), in); err != nil {
				t.Fatal("bounded conflict retained locks or consumed key", err)
			}
		})
	}
}

func TestFinancialMutationReverseReplayRequiresBothOriginalAndResult(t *testing.T) {
	for _, denied := range []string{"original", "result"} {
		t.Run(denied, func(t *testing.T) {
			f := newIntegrationFixture(t)
			category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Reverse source category"})
			create := expenseEditNewInput(t, f, store.String(category["id"]))
			create.Action, create.Lines[0].AmountEUR = "post", "10.00"
			posted, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), create)
			if err != nil {
				t.Fatal(err)
			}
			m, _ := f.app.Store.Module("expenses")
			e, _ := m.Entity("entries")
			in := auditMutation(t, f, "expenses", store.Record{}, posted.CurrentVersion)
			key := value.UUID()
			reversal, err := f.app.Store.Action(f.ctx, f.identity, m, e, posted.EntryID, "reverse", key, value.UUID(), in)
			if err != nil {
				t.Fatal(err)
			}
			beforeReplay := financialMutationSnapshot(t, f)
			if got, err := f.app.Store.Action(f.ctx, f.identity, m, e, posted.EntryID, "reverse", key, value.UUID(), in); err != nil || store.String(got["id"]) != store.String(reversal["id"]) || financialMutationSnapshot(t, f) != beforeReplay {
				t.Fatal("authorized reversal replay failed or repeated effects", got, err)
			}
			target := posted.EntryID
			if denied == "result" {
				target = store.String(reversal["id"])
			}
			if _, err = f.owner.Exec(f.ctx, "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1", target); err != nil {
				t.Fatal(err)
			}
			before := financialMutationSnapshot(t, f)
			if got, err := f.app.Store.Action(f.ctx, f.identity, m, e, posted.EntryID, "reverse", key, value.UUID(), in); !errors.Is(err, store.ErrNotFound) || got != nil || financialMutationSnapshot(t, f) != before {
				t.Fatal("reversal replay exposed a denied endpoint or repeated reversal", got, err)
			}
		})
	}
}

func TestFinancialMutationFuelDeniedOrAmbiguousExistingTargetNeverCreatesSecondEntry(t *testing.T) {
	for _, damage := range []string{"retired", "wrong type", "ambiguous", "deleted source"} {
		t.Run(damage, func(t *testing.T) {
			f := newIntegrationFixture(t)
			category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Fuel category"})
			vehicle, _ := auditCreate(t, f, "fuel", "vehicles", store.Record{"name": "Fuel privacy vehicle", "fuel_type": "diesel"})
			series, _ := auditCreate(t, f, "fuel", "odometer_series", store.Record{"vehicle_id": vehicle["id"], "started_on": "2026-01-01", "initial_reading": "0", "reason": "Synthetic series"})
			fill, _ := auditCreate(t, f, "fuel", "fillups", store.Record{"vehicle_id": vehicle["id"], "odometer_series_id": series["id"], "occurred_on": "2026-10-26", "odometer": "100", "quantity": "10", "total_minor": "20.00", "fuel_type": "diesel", "full_tank": true, "station": "Synthetic station"})
			m, _ := f.app.Store.Module("fuel")
			e, _ := m.Entity("fillups")
			in := auditMutation(t, f, "fuel", store.Record{"category_id": category["id"]}, store.Int(fill["version"]))
			in.TargetGenerations = map[string]int64{"expenses": f.app.Store.States()["expenses"].Generation}
			key := value.UUID()
			first, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(fill["id"]), "expense", key, value.UUID(), in)
			if err != nil {
				t.Fatal(err)
			}
			beforeReplay := financialMutationSnapshot(t, f)
			if got, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(fill["id"]), "expense", key, value.UUID(), in); err != nil || store.String(got["expense_id"]) != store.String(first["expense_id"]) || financialMutationSnapshot(t, f) != beforeReplay {
				t.Fatal("authorized fuel replay failed or repeated effects", got, err)
			}
			switch damage {
			case "retired":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1", first["expense_id"])
			case "wrong type":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.resource_links SET target_type='categories' WHERE source_id=$1 AND relation='fuel_expense'", fill["id"])
			case "ambiguous":
				_, err = f.owner.Exec(f.ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'fuel','fillups',$2,'expenses','categories',$3,'fuel_expense')`, f.identity.HouseholdID, fill["id"], category["id"])
			case "deleted source":
				protected := financialFuelCompatibilitySnapshot(t, f)
				proofs := fuelModuleCoreNativeSnapshot(t, f)
				if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(fill["id"]), store.Int(fill["version"]), in.Generation, value.UUID()); err == nil ||
					financialFuelCompatibilitySnapshot(t, f) != protected || fuelModuleCoreNativeSnapshot(t, f) != proofs {
					t.Fatal("public linked-source deletion must preserve original facts and accepted proofs", err)
				}
				// An owner-only same-generation restore fault removes the source;
				// it is not permission for the public API to delete linked facts.
				financialFuelCompatibilityRemoveSource(t, f, store.String(fill["id"]))
				err = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			before := financialFuelCompatibilitySnapshot(t, f)
			beforeProofs := fuelModuleCoreNativeSnapshot(t, f)
			got, actionErr := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(fill["id"]), "expense", value.UUID(), value.UUID(), in)
			switch damage {
			case "wrong type", "ambiguous":
				// The current durable binding is authoritative over a malformed
				// secondary graph, provided its exact target is still authorized.
				if actionErr != nil || store.String(got["expense_id"]) != store.String(first["expense_id"]) ||
					store.String(got["binding_id"]) != store.String(first["binding_id"]) || store.String(got["id"]) != store.String(fill["id"]) {
					t.Fatal("valid durable binding was replaced or exposed a different target", got, actionErr)
				}
			case "retired":
				if !errors.Is(actionErr, store.ErrNotFound) || got != nil {
					t.Fatal("new key exposed a currently denied financial target", got, actionErr)
				}
			case "deleted source":
				if !errors.Is(actionErr, store.ErrConflict) || got != nil {
					t.Fatal("new key recreated a missing source or exposed a new expense", got, actionErr)
				}
			}
			if financialFuelCompatibilitySnapshot(t, f) != before {
				t.Fatal("target reuse/refusal changed business facts or projections")
			}
			if (damage == "retired" || damage == "deleted source") && fuelModuleCoreNativeSnapshot(t, f) != beforeProofs {
				t.Fatal("refused new intent left a private operation or other effect")
			}
			beforeProofs = fuelModuleCoreNativeSnapshot(t, f)
			got, actionErr = f.app.Store.Action(f.ctx, f.identity, m, e, store.String(fill["id"]), "expense", key, value.UUID(), in)
			if damage == "retired" {
				if !errors.Is(actionErr, store.ErrNotFound) || got != nil {
					t.Fatal("historical receipt exposed a currently denied financial target", got, actionErr)
				}
			} else if actionErr != nil || store.String(got["expense_id"]) != store.String(first["expense_id"]) ||
				store.String(got["binding_id"]) != store.String(first["binding_id"]) || store.String(got["operation_id"]) != store.String(first["operation_id"]) {
				t.Fatal("historical accepted receipt changed identity or recreated a missing source", got, actionErr)
			}
			if financialFuelCompatibilitySnapshot(t, f) != before || fuelModuleCoreNativeSnapshot(t, f) != beforeProofs {
				t.Fatal("historical receipt replay changed business facts")
			}
			var entries, links, bindings int
			if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.entries),
 (SELECT count(*) FROM core.resource_links WHERE relation='fuel_expense'),
 (SELECT count(*) FROM fuel.integration_bindings WHERE source_id=$1 AND relation='fuel_expense')`, fill["id"]).Scan(&entries, &links, &bindings); err != nil || entries != 1 || bindings != 1 || links != map[string]int{"retired": 1, "wrong type": 1, "ambiguous": 2, "deleted source": 0}[damage] {
				t.Fatal("durable reuse/refusal created another financial identity", entries, links, bindings, err)
			}
		})
	}
}

func financialFuelCompatibilitySnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var source string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'vehicles',(SELECT coalesce(jsonb_agg(to_jsonb(v) ORDER BY id),'[]') FROM fuel.vehicles v WHERE household_id=$1),
 'series',(SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY id),'[]') FROM fuel.odometer_series s WHERE household_id=$1),
 'fillups',(SELECT coalesce(jsonb_agg(to_jsonb(f) ORDER BY id),'[]') FROM fuel.fillups f WHERE household_id=$1),
 'bindings',(SELECT coalesce(jsonb_agg(to_jsonb(b) ORDER BY id),'[]') FROM fuel.integration_bindings b WHERE household_id=$1))::text`,
		f.identity.HouseholdID).Scan(&source); err != nil {
		t.Fatal("Fuel business snapshot", err)
	}
	return source + "\n" + financialMutationSnapshot(t, f)
}

func financialFuelCompatibilityRemoveSource(t *testing.T, f *integrationFixture, fill string) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(f.ctx, "DELETE FROM fuel.fillups WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, fill); err != nil {
		t.Fatal("owner prior-source removal", err)
	}
	if _, err = tx.Exec(f.ctx, "DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='fuel' AND source_type='fillups' AND source_id=$2", f.identity.HouseholdID, fill); err != nil {
		t.Fatal("owner prior-link removal", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFinancialMutationFuelLegacyOnlyMalformedTargetNeverCreatesSecondEntry(t *testing.T) {
	for _, damage := range []string{"retired", "wrong type", "ambiguous", "deleted source"} {
		t.Run(damage, func(t *testing.T) {
			f := newIntegrationFixture(t)
			fuelModuleUpgradePriorCatalog(t, f)
			category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Genuine Fuel2 legacy category"})
			posted := value.UUID()
			vehicle, series, fill := value.UUID(), value.UUID(), value.UUID()
			ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
			tx, err := f.app.Store.BeginMutation(ctx, f.identity, "fuel", f.app.Store.States()["fuel"].Generation)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tx.Rollback(cleanup)
			}()
			// This synthetic predecessor fixture executes the real prior App SQL
			// guards, not the current Go writer against a schema without its API.
			// Draft, allocation and posting retain the old schema's real audit,
			// financial-subject, immutability and deferred-total constraints.
			if _, err = tx.Exec(ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,description,amount_minor,created_by)
 VALUES($1,$2,'2026-10-26','Genuine prior Fuel2 linked expense',2000,$3)`, posted, f.identity.HouseholdID, f.identity.UserID); err != nil {
				t.Fatal("prior App expense draft", err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,created_by)
 VALUES($1,$2,$3,2000,$4)`, f.identity.HouseholdID, posted, category["id"], f.identity.UserID); err != nil {
				t.Fatal("prior App expense allocation", err)
			}
			if _, err = tx.Exec(ctx, "UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2 AND state='draft'", f.identity.HouseholdID, posted); err != nil {
				t.Fatal("prior App expense posting", err)
			}
			// Actual prior Fuel2 schema and App authority seed prior source facts
			// and its one legacy Core link. Modern private tables do not exist.
			if _, err = tx.Exec(ctx, `INSERT INTO fuel.vehicles(id,household_id,name,fuel_type,created_by)
 VALUES($1,$2,'Genuine prior Fuel2 source','diesel',$3)`, vehicle, f.identity.HouseholdID, f.identity.UserID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO fuel.odometer_series(id,household_id,vehicle_id,started_on,initial_reading,reason,created_by)
 VALUES($1,$2,$3,'2026-01-01',0,'Prior original counter',$4)`, series, f.identity.HouseholdID, vehicle, f.identity.UserID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO fuel.fillups(id,household_id,vehicle_id,odometer_series_id,occurred_on,
 fuel_type,odometer,quantity,total_minor,unit_price,station,full_tank,notes,created_by)
 VALUES($1,$2,$3,$4,'2026-10-26','diesel',100,10,2000,2,'Prior station',true,'Prior original fact',$5)`, fill, f.identity.HouseholdID, vehicle, series, f.identity.UserID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'fuel','fillups',$2,'expenses','entries',$3,'fuel_expense')`, f.identity.HouseholdID, fill, posted); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "retired":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1", posted)
			case "wrong type":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.resource_links SET target_type='categories' WHERE source_id=$1 AND relation='fuel_expense'", fill)
			case "ambiguous":
				_, err = f.owner.Exec(f.ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'fuel','fillups',$2,'expenses','categories',$3,'fuel_expense')`, f.identity.HouseholdID, fill, category["id"])
			case "deleted source":
				// Clearly labeled owner/prior restore fault, with no fake modern
				// operations or deletion of modern binding rows.
				financialFuelCompatibilityRemoveSource(t, f, fill)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
				t.Fatal("normal Fuel2-to-current migration", err)
			}
			if err = database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal("actual upgraded readiness", err)
			}
			var bindings, operations int
			if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM fuel.integration_bindings),(SELECT count(*) FROM fuel.module_operations)").Scan(&bindings, &operations); err != nil || bindings != 0 || operations != 0 {
				t.Fatal("prior fixture fabricated a modern authoritative binding/operation", bindings, operations, err)
			}
			m, _ := f.app.Store.Module("fuel")
			e, _ := m.Entity("fillups")
			in := auditMutation(t, f, "fuel", store.Record{"category_id": category["id"]}, 1)
			in.TargetGenerations = map[string]int64{"expenses": f.app.Store.States()["expenses"].Generation}
			before := financialFuelCompatibilitySnapshot(t, f)
			proofs := fuelModuleCoreNativeSnapshot(t, f)
			got, actionErr := f.app.Store.Action(f.ctx, f.identity, m, e, fill, "expense", value.UUID(), value.UUID(), in)
			want := store.ErrNotFound
			if damage == "deleted source" {
				want = store.ErrConflict
			}
			if !errors.Is(actionErr, want) || got != nil || financialFuelCompatibilitySnapshot(t, f) != before || fuelModuleCoreNativeSnapshot(t, f) != proofs {
				t.Fatal("legacy-only invalid target created an entry/binding or leaked a receipt", got, actionErr)
			}
			var entries, links int
			if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.entries),
 (SELECT count(*) FROM core.resource_links WHERE relation='fuel_expense'),
 (SELECT count(*) FROM fuel.integration_bindings),(SELECT count(*) FROM fuel.module_operations)`).Scan(&entries, &links, &bindings, &operations); err != nil ||
				entries != 1 || bindings != 0 || operations != 0 || links != map[string]int{"retired": 1, "wrong type": 1, "ambiguous": 2, "deleted source": 0}[damage] {
				t.Fatal("legacy-only denial produced a second financial identity", entries, links, bindings, operations, err)
			}
		})
	}
}
