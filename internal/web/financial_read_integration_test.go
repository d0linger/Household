//go:build integration

package web

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
)

func financialReadsDenied(t *testing.T, f *integrationFixture, id auth.Identity, entry, line string, expected error) {
	t.Helper()
	m, _ := f.app.Store.Module("expenses")
	for _, target := range []struct{ entity, id string }{{"entries", entry}, {"entry_lines", line}} {
		e, _ := m.Entity(target.entity)
		if record, err := f.app.Store.Get(f.ctx, id, m, e, target.id); !errors.Is(err, expected) || record != nil {
			t.Fatal("denied direct financial read returned content", target.entity, record, err)
		}
		rows, err := f.app.Store.List(f.ctx, id, m, e, "", 50, 0)
		if errors.Is(expected, store.ErrNotFound) {
			if err != nil || len(rows) != 0 {
				t.Fatal("denied financial record entered a list", target.entity, rows, err)
			}
		} else if !errors.Is(err, expected) || rows != nil {
			t.Fatal("stale scope produced a list", rows, err)
		}
	}
	if bundle, err := f.app.Store.ExpenseBundle(f.ctx, id, entry); !errors.Is(err, expected) || !reflect.DeepEqual(bundle, store.ExpenseBundle{}) {
		t.Fatal("denied bundle leaked partial facts or a size error", bundle, err)
	}
}

func TestFinancialReadCurrentSharedMemberAndActualAccessRevision(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	reader := shoppingTestMember(t, f, "financial-current-reader", "reader")
	_, reader, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "Current financial reader")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	var contentVersion int64
	for _, id := range []auth.Identity{f.identity, reader} {
		for _, target := range []struct{ entity, id string }{{"entries", store.String(entry["id"])}, {"entry_lines", store.String(line["id"])}} {
			e, _ := m.Entity(target.entity)
			if got, err := f.app.Store.Get(f.ctx, id, m, e, target.id); err != nil || store.String(got["id"]) != target.id {
				t.Fatal("current member lost a shared direct record", got, err)
			}
			if got, err := f.app.Store.List(f.ctx, id, m, e, "", 50, 0); err != nil || len(got) != 1 || store.String(got[0]["id"]) != target.id {
				t.Fatal("current member lost a shared list record", got, err)
			}
		}
		got, err := f.app.Store.ExpenseBundle(f.ctx, id, store.String(entry["id"]))
		if err != nil || got.AccessVersion != 1 || got.Editable != id.CanWrite() || got.Header.AmountMinor != "1000" {
			t.Fatal("bundle access revision, role or exact facts differ", got, err)
		}
		contentVersion = got.Header.Version
	}
	policy := financialPolicyID(t, f, entry)
	if err := expenseIntegrityAppSQL(t, f, "UPDATE core.financial_policies SET version=version+1 WHERE id=$1", policy); err != nil {
		t.Fatal(err)
	}
	got, err := f.app.Store.ExpenseBundle(f.ctx, reader, store.String(entry["id"]))
	if err != nil || got.AccessVersion != 2 || got.Header.Version != contentVersion {
		t.Fatal("access revision was inferred from content version", got, err)
	}
	newForm, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, "")
	if err != nil || newForm.AccessVersion != 0 {
		t.Fatal("new form fabricated an existing policy revision", newForm, err)
	}
}

func TestFinancialReadDamagedRequiredBindingsDenyEntireRecord(t *testing.T) {
	for _, test := range []struct {
		name, statement string
		child           bool
	}{
		{"missing parent subject", "DELETE FROM core.financial_subjects WHERE resource_type='entries' AND resource_id=$1", false},
		{"missing child subject", "DELETE FROM core.financial_subjects WHERE resource_type='entry_lines' AND resource_id=$1", true},
		{"retired policy", "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1", false},
		{"retired child", "UPDATE core.financial_subjects SET retired_at=clock_timestamp() WHERE resource_type='entry_lines' AND resource_id=$1", true},
		{"old policy generation", "UPDATE core.financial_policies SET module_generation=module_generation+1 WHERE resource_id=$1", false},
		{"old subject generation", "UPDATE core.financial_subjects SET module_generation=module_generation+1 WHERE resource_id=$1", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _, entry, line := expenseIntegrityFixture(t)
			// Owner corruption models a broken restored binding. Runtime guards
			// separately reject these mutations; no missing-policy fallback is safe.
			target := entry["id"]
			if test.child {
				target = line["id"]
			}
			if _, err := f.owner.Exec(f.ctx, test.statement, target); err != nil {
				t.Fatal(err)
			}
			financialReadsDenied(t, f, f.identity, store.String(entry["id"]), store.String(line["id"]), store.ErrNotFound)
		})
	}
}

func TestFinancialReadWrongChildPolicyAndUnseen101stBindingDenyBeforeContent(t *testing.T) {
	f, category, entry, line := expenseIntegrityFixture(t)
	other, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Other policy", "amount_minor": "1.00"})
	otherPolicy := financialPolicyID(t, f, other)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.financial_subjects SET policy_id=$2 WHERE resource_type='entry_lines' AND resource_id=$1", line["id"], otherPolicy); err != nil {
		t.Fatal(err)
	}
	if bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"])); !errors.Is(err, store.ErrNotFound) || !reflect.DeepEqual(bundle, store.ExpenseBundle{}) {
		t.Fatal("wrong-policy child produced a partial sum", bundle, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.financial_subjects SET policy_id=$2 WHERE resource_type='entry_lines' AND resource_id=$1", line["id"], financialPolicyID(t, f, entry)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
 SELECT $1,$2,$3,1,repeat('x',11000),n,$4 FROM generate_series(1,100) n`, f.identity.HouseholdID, entry["id"], category["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `DELETE FROM core.financial_subjects WHERE resource_type='entry_lines' AND resource_id IN
 (SELECT id FROM expenses.entry_lines WHERE entry_id=$1 AND position=100)`, entry["id"]); err != nil {
		t.Fatal(err)
	}
	if bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"])); !errors.Is(err, store.ErrNotFound) || !reflect.DeepEqual(bundle, store.ExpenseBundle{}) {
		t.Fatal("invisible101st binding was ignored or leaked oversized-content information", bundle, err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entry_lines")
	if rows, err := f.app.Store.List(f.ctx, f.identity, m, e, "", 100, 0); err != nil || len(rows) != 0 {
		t.Fatal("partial financial child set escaped through generic list", len(rows), err)
	}
}

func TestFinancialReadFiltersBeforeLimitAndOffset(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	second, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Second visible", "amount_minor": "2.00"})
	hidden, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Hidden newest", "amount_minor": "3.00"})
	if _, err := f.owner.Exec(f.ctx, `UPDATE expenses.entries SET created_at=CASE id WHEN $1 THEN '2026-01-01'::timestamptz
 WHEN $2 THEN '2026-01-02'::timestamptz WHEN $3 THEN '2026-01-03'::timestamptz ELSE created_at END WHERE household_id=$4`, entry["id"], second["id"], hidden["id"], f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1", hidden["id"]); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	for offset, want := range []string{store.String(second["id"]), store.String(entry["id"])} {
		rows, err := f.app.Store.List(f.ctx, f.identity, m, e, "", 1, offset)
		if err != nil || len(rows) != 1 || store.String(rows[0]["id"]) != want {
			t.Fatal("hidden-first pagination lost a visible row or offset", offset, rows, err)
		}
	}
	if rows, err := f.app.Store.List(f.ctx, f.identity, m, e, "Hidden", 1, 0); err != nil || len(rows) != 0 {
		t.Fatal("primary-field query bypassed visibility", rows, err)
	}
}

func TestFinancialReadChecksCurrentSessionMembershipAndModule(t *testing.T) {
	for _, test := range []struct {
		name, statement, target string
		expected                error
	}{
		{"revoked session", "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", "session", store.ErrForbidden},
		{"expired idle session", "UPDATE core.sessions SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "session", store.ErrForbidden},
		{"inactive membership", "UPDATE core.memberships SET inactive_at=clock_timestamp() WHERE id=$1", "membership", store.ErrForbidden},
		{"changed role", "UPDATE core.memberships SET role='reader' WHERE id=$1", "membership", store.ErrForbidden},
		{"disabled expenses", "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='expenses'", "household", store.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _, entry, line := expenseIntegrityFixture(t)
			target := f.identity.SessionID
			switch test.target {
			case "membership":
				target = f.identity.MembershipID
			case "household":
				target = f.identity.HouseholdID
			}
			if _, err := f.owner.Exec(f.ctx, test.statement, target); err != nil {
				t.Fatal(err)
			}
			if errors.Is(test.expected, store.ErrNotFound) {
				m, _ := f.app.Store.Module("expenses")
				e, _ := m.Entity("entries")
				if got, err := f.app.Store.List(f.ctx, f.identity, m, e, "", 50, 0); !errors.Is(err, store.ErrNotFound) || got != nil {
					t.Fatal("cached enabled state authorized current list", got, err)
				}
				return
			}
			financialReadsDenied(t, f, f.identity, store.String(entry["id"]), store.String(line["id"]), test.expected)
		})
	}
}

func TestFinancialReadForeignIdentityAndCancellationReturnNoPartialData(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if got, err := f.app.Store.Get(f.ctx, foreign, m, e, store.String(entry["id"])); !errors.Is(err, store.ErrForbidden) || got != nil {
		t.Fatal("foreign household scope read financial facts", got, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if got, err := f.app.Store.List(ctx, f.identity, m, e, "", 50, 0); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("cancelled list returned partial data", got, err)
	}
	if err := f.pool.Ping(f.ctx); err != nil {
		t.Fatal("cancelled financial read leaked its connection", err)
	}
}
