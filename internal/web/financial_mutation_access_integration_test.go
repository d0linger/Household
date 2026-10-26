//go:build integration

package web

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.financial_subjects WHERE resource_type='entry_lines' AND resource_id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	before := financialMutationSnapshot(t, f)
	in := auditMutation(t, f, "expenses", store.Record{}, store.Int(current["version"]))
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
				err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(fill["id"]), store.Int(fill["version"]), in.Generation, value.UUID())
			}
			if err != nil {
				t.Fatal(err)
			}
			before := financialMutationSnapshot(t, f)
			if got, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(fill["id"]), "expense", value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrNotFound) || got != nil || financialMutationSnapshot(t, f) != before {
				t.Fatal("denied or ambiguous existing fuel link fell through to new entry", got, err)
			}
			if damage == "retired" || damage == "deleted source" {
				if got, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(fill["id"]), "expense", key, value.UUID(), in); !errors.Is(err, store.ErrNotFound) || got != nil || financialMutationSnapshot(t, f) != before {
					t.Fatal("fuel action replay returned denied target", got, err)
				}
			}
			var entries, links int
			if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM expenses.entries),(SELECT count(*) FROM core.resource_links WHERE relation='fuel_expense')").Scan(&entries, &links); err != nil || entries != 1 || links != map[string]int{"retired": 1, "wrong type": 1, "ambiguous": 2, "deleted source": 0}[damage] {
				t.Fatal("fuel denial created a duplicate financial identity", entries, links, err)
			}
		})
	}
}
