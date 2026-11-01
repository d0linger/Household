//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"testing"

	"household/internal/audit"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFinanceReplayOnlyNeverExecutesMissingOrChangedIntent(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	rid := value.UUID()
	in := Record{"name": "Replay-only source sentinel", "account_type": "bank", "currency": "EUR", "exponent": 2, "notes": "Explicit real native command"}
	c, key, _ := financeNativePreview(t, f, "accounts", "save", rid, 0, in)
	if _, err := f.store.FinanceReplay(f.ctx, f.identity, key, c); !errors.Is(err, ErrNotFound) {
		t.Fatal("replay-only path executed or accepted an uncommitted preview", err)
	}
	var count int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM expenses.accounts WHERE household_id=$1`, f.identity.HouseholdID).Scan(&count); err != nil || count != 0 {
		t.Fatal("missing receipt replay created financial facts", count, err)
	}
	accepted, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.FinanceReplay(f.ctx, f.identity, key, c)
	if err != nil || replay["replayed"] != true || replay["resource_id"] != accepted["resource_id"] {
		t.Fatal("authentic accepted intent was not readable without execution", replay, err)
	}
	changed := c
	in["notes"] = "A different intent cannot borrow approval"
	changed.Payload, err = json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinanceReplay(f.ctx, f.identity, key, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("different canonical body borrowed a receipt", err)
	}
	if _, err = f.store.FinanceReplay(f.ctx, f.identity, value.UUID(), c); !errors.Is(err, ErrNotFound) {
		t.Fatal("fresh key was executed from an expired-review path", err)
	}
	reader := financeAuthorityMember(t, f, "reader")
	if _, err = f.store.FinanceReplay(f.ctx, reader, key, c); !errors.Is(err, ErrNotFound) {
		t.Fatal("another actor borrowed accepted operation identity", err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM expenses.accounts WHERE household_id=$1`, f.identity.HouseholdID).Scan(&count); err != nil || count != 1 {
		t.Fatal("read-only replay duplicated or changed business count", count, err)
	}
}

func TestFinanceOwnerDefinerCannotRewritePostedLegacyHeader(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category, entry := financeNativeCategory(t, f), value.UUID()
	in := financeNativeEntry(category, "100")
	financeNativeApply(t, f, "entries", "post", entry, 0, in)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.test_finance_posted_owner_frame(p_id uuid) RETURNS void
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
 BEGIN UPDATE expenses.entries SET description='forbidden owner-frame rewrite' WHERE id=p_id;END $$;
 REVOKE ALL ON FUNCTION core.test_finance_posted_owner_frame(uuid) FROM PUBLIC;
 GRANT EXECUTE ON FUNCTION core.test_finance_posted_owner_frame(uuid) TO household_app`); err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.store.BeginMutation(ctx, f.identity, "expenses", f.store.States()["expenses"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareFinanceOrigin(ctx, tx, f.identity, scope, "entries", entry, "manual", nil); err != nil {
		t.Fatal("real current actor must reach the immutable fact guard", err)
	}
	_, err = tx.Exec(ctx, "SELECT core.test_finance_posted_owner_frame($1)", entry)
	if problem, ok := errors.AsType[*pgconn.PgError](err); !ok || problem.Code != "23514" || problem.Message != "posted entry is immutable" {
		t.Fatal("Owner frame must reach the exact posted immutability guard", err)
	}
	rollback(tx)
	var description string
	if err = f.owner.QueryRow(f.ctx, `SELECT description FROM expenses.entries WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, entry).Scan(&description); err != nil || description != in.Description {
		t.Fatal("denied Owner frame changed posted original", description, err)
	}
}
