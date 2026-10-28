//go:build integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func barcodeFixture(t *testing.T, f *integrationFixture) (string, store.Record, store.ShoppingBarcodeInput) {
	t.Helper()
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Reviewed barcode list"})
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Local oat pack", "brand": "Actual local catalogue", "pack_quantity": "500", "pack_unit": "g"})
	input := store.ShoppingBarcodeInput{Generation: f.app.Store.States()["shopping"].Generation, ListVersion: store.Int(list["version"]), Code: "00073AbC", ProductID: store.String(product["id"]), ProductVersion: store.Int(product["version"])}
	return store.String(list["id"]), product, input
}

func barcodeMap(t *testing.T, f *integrationFixture, list string, in store.ShoppingBarcodeInput) store.ShoppingBarcodeInput {
	t.Helper()
	alias, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.AliasID, in.AliasVersion = store.String(alias["id"]), store.Int(alias["alias_version"])
	return in
}

func barcodeSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var rows string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('aliases',(SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM shopping.barcodes b),
 'products',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM shopping.products p),
 'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM shopping.items i),
 'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY user_id,operation,key) FROM core.idempotency_records r),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM core.audit_log a),
 'search',(SELECT jsonb_agg(to_jsonb(d) ORDER BY module_id,resource_type,resource_id) FROM core.search_documents d),
 'outbox',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM core.outbox_events e))::text`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestShoppingBarcodeReviewedMappingLookupAddAndReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	before := barcodeSnapshot(t, f)
	unknown, err := f.app.Store.ShoppingBarcode(f.ctx, f.identity, list, " 00073AbC ")
	if err != nil || unknown.Product != nil || unknown.Code != input.Code || barcodeSnapshot(t, f) != before {
		t.Fatal("lookup fabricated a mapping or mutation", unknown, err)
	}
	key, request := value.UUID(), value.UUID()
	alias, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", key, request, input)
	if err != nil {
		t.Fatal(err)
	}
	afterMap := barcodeSnapshot(t, f)
	if replay, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", key, value.UUID(), input); err != nil || replay["id"] != alias["id"] || barcodeSnapshot(t, f) != afterMap {
		t.Fatal("map replay duplicated effects", err)
	}
	var audited bool
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*)=1 AND bool_and(actor_user_id=$2::uuid AND db_role='household_app' AND changes->'code'->>'after'='***REDACTED***')
 FROM core.audit_log WHERE request_id=$1 AND module_id='shopping' AND resource_type='barcodes' AND action='CREATE'`, request, f.identity.UserID).Scan(&audited); err != nil || !audited {
		t.Fatal("actual alias change lacks redacted actor audit", err)
	}
	view, err := f.app.Store.ShoppingBarcode(f.ctx, f.identity, list, input.Code)
	if err != nil || view.Product == nil || view.Product.ID != input.ProductID || view.Product.PackQuantity != "500.000000" {
		t.Fatal("typed local pack lookup differs", view, err)
	}
	lower, err := f.app.Store.ShoppingBarcode(f.ctx, f.identity, list, strings.ToLower(input.Code))
	if err != nil || lower.Product != nil {
		t.Fatal("case-distinct internal code collapsed", err)
	}
	input.AliasID, input.AliasVersion, input.Quantity = view.AliasID, view.AliasVersion, "2"
	addKey := value.UUID()
	item, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "add", addKey, value.UUID(), input)
	if err != nil {
		t.Fatal(err)
	}
	afterAdd := barcodeSnapshot(t, f)
	if replay, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "add", addKey, value.UUID(), input); err != nil || replay["id"] != item["id"] || barcodeSnapshot(t, f) != afterAdd {
		t.Fatal("add replay duplicated effects", err)
	}
	var correct bool
	if err = f.pool.QueryRow(f.ctx, `SELECT i.quantity=2 AND i.unit_code='piece' AND i.checked_at IS NULL
 AND EXISTS(SELECT 1 FROM core.search_documents WHERE household_id=i.household_id AND module_id='shopping' AND resource_type='items' AND resource_id=i.id)
 AND (SELECT count(*)=1 FROM core.outbox_events o WHERE o.household_id=i.household_id AND o.module_id='shopping'
  AND o.module_generation=$3 AND o.event_type='record_changed' AND o.actor_user_id=$4::uuid
  AND o.payload->>'entity'='items' AND o.payload->>'id'=i.id::text)
 AND NOT EXISTS(SELECT 1 FROM shopping.checkouts WHERE list_id=i.list_id)
 FROM shopping.items i WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, item["id"], input.Generation, f.identity.UserID).Scan(&correct); err != nil || !correct {
		t.Fatal("reviewed item/projections differ or purchase was automatic", err)
	}
	if _, err = f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "add", value.UUID(), value.UUID(), input); !errors.Is(err, store.ErrConflict) || barcodeSnapshot(t, f) != afterAdd {
		t.Fatal("new-key duplicate product was added", err)
	}
}

func TestShoppingBarcodeCurrentScopePrecedesEveryReplay(t *testing.T) {
	for _, damage := range []string{"reader", "revoked", "auth version", "inactive", "foreign household", "disabled", "generation", "list version", "product version", "template", "archived", "completed reopened", "missing list"} {
		t.Run(damage, func(t *testing.T) {
			f := newIntegrationFixture(t)
			list, _, input := barcodeFixture(t, f)
			key := value.UUID()
			if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", key, value.UUID(), input); err != nil {
				t.Fatal(err)
			}
			actor := f.identity
			var err error
			switch damage {
			case "reader":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.memberships SET role='reader' WHERE id=$1", actor.MembershipID)
				actor.Role = "reader"
			case "revoked":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=now() WHERE id=$1", actor.SessionID)
			case "auth version":
				actor.AuthVersion++
			case "inactive":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.memberships SET inactive_at=now() WHERE id=$1", actor.MembershipID)
			case "foreign household":
				actor.HouseholdID = value.UUID()
			case "disabled":
				_, err = f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='shopping'", actor.HouseholdID)
			case "generation":
				input.Generation++
			case "list version":
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET version=version+1 WHERE id=$1", list)
			case "product version":
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.products SET name='New approved label',version=version+1 WHERE id=$1", input.ProductID)
			case "template":
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET is_template=true WHERE id=$1", list)
			case "archived":
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=now() WHERE id=$1", list)
			case "completed reopened":
				if _, err = f.app.Store.ShoppingBarcode(f.ctx, actor, list, input.Code); err == nil {
					// Actual checkout, followed by a normal owner fixture reopen, must
					// still reject replay of the already accepted map receipt.
					item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list, "name": "Reviewed", "quantity": "1", "unit_code": "piece"})
					m, _ := f.app.Store.Module("shopping")
					e, _ := m.Entity("items")
					_, err = f.app.Store.Action(f.ctx, actor, m, e, store.String(item["id"]), "check", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"checked": true}, store.Int(item["version"])))
					if err == nil {
						var preview store.Record
						preview, err = f.app.Store.ShoppingCheckoutPreview(f.ctx, actor, list)
						if err == nil {
							_, err = f.app.Store.CheckoutShopping(f.ctx, actor, list, value.UUID(), value.UUID(), store.CheckoutInput{Generation: input.Generation, Fingerprint: store.String(preview["fingerprint"]), ReceivedOn: store.String(preview["today"]), Lines: []store.CheckoutLine{{ItemID: store.String(item["id"]), Decision: "no_stock", Quantity: "1", Unit: "piece"}}})
						}
					}
				}
				if err == nil {
					_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=NULL WHERE id=$1", list)
				}
			case "missing list":
				list = value.UUID()
			}
			if err != nil {
				t.Fatal(err)
			}
			before := barcodeSnapshot(t, f)
			if _, err = f.app.Store.MutateShoppingBarcode(f.ctx, actor, list, "map", key, value.UUID(), input); err == nil || barcodeSnapshot(t, f) != before {
				t.Fatal("current-scope failure delivered stale receipt or effects", err)
			}
		})
	}
}

func TestShoppingBarcodePackIdentityAndSQLCapacity(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	mapped := barcodeMap(t, f, list, input)
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("products")
	before := barcodeSnapshot(t, f)
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, e, input.ProductID, value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"pack_quantity": "750"}, input.ProductVersion)); err == nil || barcodeSnapshot(t, f) != before {
		t.Fatal("generic product edit changed mapped pack", err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE shopping.products SET pack_unit='kg' WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, input.ProductID); err == nil {
		t.Fatal("SQL path changed mapped pack")
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE shopping.barcodes SET code='replacement' WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, mapped.AliasID); err == nil {
		t.Fatal("runtime rewrote immutable alias")
	}
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "shopping", input.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO shopping.barcodes(household_id,product_id,code,created_by) SELECT $1,$2,'capacity-'||n,$3 FROM generate_series(1,99) n`, f.identity.HouseholdID, input.ProductID, f.identity.UserID); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	before = barcodeSnapshot(t, f)
	input.Code = "capacity-101"
	if _, err = f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", value.UUID(), value.UUID(), input); err == nil || barcodeSnapshot(t, f) != before {
		t.Fatal("101st alias accepted or left effects", err)
	}
}

func TestShoppingBarcodeOnlyIDRowLockGrantCannotRewriteAlias(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	input = barcodeMap(t, f, list, input)
	var precise bool
	if err := f.pool.QueryRow(f.ctx, `SELECT current_user='household_app'
 AND has_column_privilege(current_user,'shopping.barcodes','id','UPDATE')
 AND NOT has_table_privilege(current_user,'shopping.barcodes','UPDATE')
 AND NOT has_column_privilege(current_user,'shopping.barcodes','code','UPDATE')
 AND NOT has_column_privilege(current_user,'shopping.barcodes','product_id','UPDATE')
 AND NOT has_column_privilege(current_user,'shopping.barcodes','version','UPDATE')`).Scan(&precise); err != nil || !precise {
		t.Fatal("row-lock privilege was broadened beyond immutable id", precise, err)
	}
	before := barcodeSnapshot(t, f)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.app.Store.BeginMutation(ctx, f.identity, "shopping", input.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var actual string
	if err = tx.QueryRow(ctx, "SELECT id FROM shopping.barcodes WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", f.identity.HouseholdID, input.AliasID).Scan(&actual); err != nil || actual != input.AliasID {
		t.Fatal("actual App could not acquire the reviewed alias fence", actual, err)
	}
	_, err = tx.Exec(ctx, "UPDATE shopping.barcodes SET id=id WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, input.AliasID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "23514" || !strings.Contains(failure.Message, "assignment is immutable") {
		t.Fatal("the narrow grant allowed an actual no-op UPDATE or merely failed ACL checking", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE shopping.barcodes SET code=code WHERE household_id=$1 AND id=$2",
		"UPDATE shopping.barcodes SET product_id=product_id WHERE household_id=$1 AND id=$2",
		"UPDATE shopping.barcodes SET version=version WHERE household_id=$1 AND id=$2",
	} {
		_, err = f.pool.Exec(f.ctx, statement, f.identity.HouseholdID, input.AliasID)
		if !errors.As(err, &failure) || failure.Code != "42501" {
			t.Fatal("an ungranted alias column acquired UPDATE permission", err)
		}
	}
	if barcodeSnapshot(t, f) != before {
		t.Fatal("row-lock permission or rejected updates changed alias facts or audit")
	}
}

func TestShoppingBarcodeRepeatableReadSnapshotCannotExceedSQLCapacity(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	initial, err := f.app.Store.BeginMutation(ctx, f.identity, "shopping", input.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = initial.Exec(ctx, `INSERT INTO shopping.barcodes(household_id,product_id,code,created_by)
 SELECT $1::uuid,$2::uuid,'snapshot-capacity-'||n,$3::uuid FROM generate_series(1,99) n`, f.identity.HouseholdID, input.ProductID, f.identity.UserID); err != nil {
		_ = initial.Rollback(ctx)
		t.Fatal(err)
	}
	if err = initial.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	stale, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = stale.Rollback(cleanup)
	}()
	if err = audit.Bind(ctx, stale); err != nil {
		t.Fatal(err)
	}
	var role string
	var priorCount int
	if err = stale.QueryRow(ctx, "SELECT current_user,(SELECT count(*) FROM shopping.barcodes WHERE household_id=$1 AND product_id=$2)", f.identity.HouseholdID, input.ProductID).Scan(&role, &priorCount); err != nil || role != "household_app" || priorCount != 99 {
		t.Fatal("direct App snapshot did not observe the actual99 aliases", role, priorCount, err)
	}
	input.Code = "committed-alias-100"
	_ = barcodeMap(t, f, list, input)
	before := barcodeSnapshot(t, f)
	_, err = stale.Exec(ctx, "INSERT INTO shopping.barcodes(household_id,product_id,code,created_by) VALUES($1,$2,'stale-alias-101',$3)", f.identity.HouseholdID, input.ProductID, f.identity.UserID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "23514" || !strings.Contains(failure.Message, "read committed") {
		t.Fatal("stale snapshot did not receive the explicit isolation refusal", err)
	}
	if err = stale.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if barcodeSnapshot(t, f) != before {
		t.Fatal("refused stale snapshot changed aliases, audits or accepted operation results")
	}
}

func TestShoppingBarcodeRepeatableReadSnapshotCannotChangeMappedPack(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	stale, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = stale.Rollback(cleanup)
	}()
	if err = audit.Bind(ctx, stale); err != nil {
		t.Fatal(err)
	}
	var role string
	var priorCount int
	if err = stale.QueryRow(ctx, "SELECT current_user,(SELECT count(*) FROM shopping.barcodes WHERE household_id=$1 AND product_id=$2)", f.identity.HouseholdID, input.ProductID).Scan(&role, &priorCount); err != nil || role != "household_app" || priorCount != 0 {
		t.Fatal("direct App snapshot did not precede the actual first alias", role, priorCount, err)
	}
	_ = barcodeMap(t, f, list, input)
	before := barcodeSnapshot(t, f)
	_, err = stale.Exec(ctx, "UPDATE shopping.products SET pack_quantity=750 WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, input.ProductID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "23514" || !strings.Contains(failure.Message, "read committed") {
		t.Fatal("stale product snapshot did not receive the explicit isolation refusal", err)
	}
	if err = stale.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if barcodeSnapshot(t, f) != before {
		t.Fatal("refused stale product snapshot changed mapped identity or audit")
	}
}

func TestShoppingBarcodeRemoveReassignAndActualPurge(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	mapped := barcodeMap(t, f, list, input)
	key := value.UUID()
	if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "remove", key, value.UUID(), mapped); err != nil {
		t.Fatal(err)
	}
	before := barcodeSnapshot(t, f)
	if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "remove", key, value.UUID(), mapped); err != nil || barcodeSnapshot(t, f) != before {
		t.Fatal("remove replay changed effects", err)
	}
	other, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Explicit replacement pack", "pack_quantity": "1", "pack_unit": "piece"})
	input.ProductID, input.ProductVersion = store.String(other["id"]), store.Int(other["version"])
	_ = barcodeMap(t, f, list, input)
	if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "remove", key, value.UUID(), mapped); err == nil {
		t.Fatal("old remove receipt ignored a new reviewed assignment")
	}
	reservationPurgeModule(t, f, "shopping")
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.barcodes").Scan(&count); err != nil || count != 0 {
		t.Fatal("real Shopping purge left aliases", count, err)
	}
	if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", value.UUID(), value.UUID(), input); err == nil {
		t.Fatal("old-generation draft recreated purged catalogue")
	}
}

func TestShoppingBarcodeLateFailureRollsBackAliasItemAndKey(t *testing.T) {
	for _, intent := range []string{"map", "add"} {
		t.Run(intent, func(t *testing.T) {
			f := newIntegrationFixture(t)
			list, _, input := barcodeFixture(t, f)
			if intent == "add" {
				input = barcodeMap(t, f, list, input)
				input.Quantity = "1"
			}
			before := barcodeSnapshot(t, f)
			if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.barcode_test_abort() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'late barcode fixture abort' USING ERRCODE='23514';END $$;
 CREATE CONSTRAINT TRIGGER barcode_test_abort AFTER INSERT ON core.idempotency_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.barcode_test_abort()`); err != nil {
				t.Fatal(err)
			}
			key := value.UUID()
			if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, intent, key, value.UUID(), input); err == nil || barcodeSnapshot(t, f) != before {
				t.Fatal("late abort left a partial write or consumed key", err)
			}
			if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER barcode_test_abort ON core.idempotency_records; DROP FUNCTION core.barcode_test_abort()"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, intent, key, value.UUID(), input); err != nil {
				t.Fatal("rolled back key was not reusable", err)
			}
		})
	}
}

func TestShoppingBarcodeConcurrentDuplicateAndSingleConnectionLookup(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	otherList, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Concurrent reviewed list"})
	otherProduct, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Other actual pack", "pack_quantity": "1", "pack_unit": "piece"})
	inputs := []store.ShoppingBarcodeInput{input, input}
	inputs[1].ProductID, inputs[1].ProductVersion, inputs[1].ListVersion = store.String(otherProduct["id"]), store.Int(otherProduct["version"]), store.Int(otherList["version"])
	lists := []string{list, store.String(otherList["id"])}
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	results, start := make(chan error, 2), make(chan struct{})
	var workers sync.WaitGroup
	for n := range 2 {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			select {
			case <-start:
			case <-ctx.Done():
				return
			}
			_, err := f.app.Store.MutateShoppingBarcode(ctx, f.identity, lists[n], "map", value.UUID(), value.UUID(), inputs[n])
			results <- err
		}(n)
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			t.Error("barcode workers did not stop before teardown")
		}
	})
	close(start)
	success := 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				success++
			} else if !errors.Is(err, store.ErrConflict) {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent local assignment stalled")
		}
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.barcodes WHERE household_id=$1 AND code=$2", f.identity.HouseholdID, input.Code).Scan(&count); err != nil || success != 1 || count != 1 {
		t.Fatal("same household code has multiple assignments", success, count, err)
	}
	pool, err := database.Open(f.ctx, os.Getenv("HOUSEHOLD_TEST_APP_URL"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reader := &store.Store{Pool: pool}
	if view, err := reader.ShoppingBarcode(f.ctx, f.identity, list, input.Code); err != nil || view.Product == nil {
		t.Fatal("one-connection current read leaked rows", err)
	}
}

func TestShoppingBarcodeHTTPPolicyCSRFAndInertCode(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	input.Code = "<script>inert</script>"
	_ = barcodeMap(t, f, list, input)
	for _, route := range []string{"/m/shopping/lists/" + list, "/m/shopping/lists/" + list + "/barcode", "/api/v1/shopping/lists/" + list + "/barcode?code=%3Cscript%3Einert%3C%2Fscript%3E"} {
		response, raw, err := f.request(http.MethodGet, route, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatal(route, response, err)
		}
		want := "camera=()"
		if strings.HasSuffix(route, "/barcode") {
			want = "camera=(self)"
		}
		if !strings.Contains(response.Header.Get("Permissions-Policy"), want) || bytes.Contains(raw, []byte(input.Code)) && !strings.HasPrefix(route, "/api/") {
			t.Fatal("camera leaked to other documents or raw code became markup", route)
		}
		if localMedia := strings.Contains(response.Header.Get("Content-Security-Policy"), "media-src 'self' blob:"); localMedia != (want == "camera=(self)") {
			t.Fatal("local video policy escaped the authorized camera document", route)
		}
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodPost, "/api/v1/shopping/lists/"+list+"/barcode/map", f.token, "", "application/json", bytes.NewReader(body), nil)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("missing CSRF accepted barcode mutation", response, err)
	}
}

func TestShoppingBarcodeIndependentModulesAndUnsupportedIdentity(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _, input := barcodeFixture(t, f)
	_ = barcodeMap(t, f, list, input)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id IN('pantry','expenses')", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if view, err := f.app.Store.ShoppingBarcode(f.ctx, f.identity, list, input.Code); err != nil || view.Product == nil {
		t.Fatal("unrelated optional modules blocked Shopping barcode", err)
	}
	before := barcodeSnapshot(t, f)
	for _, code := range []string{"", strings.Repeat("A", 65), "two codes", "ä"} {
		input.Code = code
		if _, err := f.app.Store.MutateShoppingBarcode(f.ctx, f.identity, list, "map", value.UUID(), value.UUID(), input); err == nil || barcodeSnapshot(t, f) != before {
			t.Fatal("unsupported code mutated local data", fmt.Sprint(code), err)
		}
	}
}

func barcodeRestoreShopping7(t *testing.T, f *integrationFixture) {
	t.Helper()
	var coreVersion int
	if err := f.owner.QueryRow(f.ctx, "SELECT version FROM core.schema_migrations").Scan(&coreVersion); err != nil {
		t.Fatal(err)
	}
	if coreVersion == 26 {
		var definition string
		if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('core.audit_ready()'::regprocedure)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if strings.Count(definition, "count(*)=78") != 1 {
			t.Fatal("fixture did not retain the exact Core26 census")
		}
		if _, err := f.owner.Exec(f.ctx, strings.Replace(definition, "count(*)=78", "count(*)=77", 1)); err != nil {
			t.Fatal(err)
		}
	} else if coreVersion >= 32 {
		var supported bool
		if err := f.owner.QueryRow(f.ctx, "SELECT core.shopping_barcode_audit_supported()").Scan(&supported); err != nil || !supported {
			t.Fatal("newer core lacks actual approved barcode audit support", coreVersion, err)
		}
	} else {
		t.Fatal("unsupported populated-upgrade fixture core", coreVersion)
	}
	for _, query := range []string{
		"DROP FUNCTION shopping.barcode_ready()",
		"DROP TRIGGER barcode_product_pack ON shopping.products",
		"DROP TABLE shopping.barcodes",
		"DROP FUNCTION shopping.guard_barcode()",
		"DROP FUNCTION shopping.guard_barcode_pack()",
		"DELETE FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes'",
		"UPDATE shopping.schema_migrations SET version=7",
	} {
		if _, err := f.owner.Exec(f.ctx, query); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShoppingBarcodePopulatedUpgradePreservesPriorCatalogueAndAudits(t *testing.T) {
	f := newIntegrationFixture(t)
	list, product, _ := barcodeFixture(t, f)
	_, _ = auditCreate(t, f, "shopping", "items", store.Record{"list_id": list, "name": "Prior product item", "product_id": product["id"], "quantity": "1", "unit_code": "piece"})
	var before, after string
	query := `SELECT jsonb_build_object('products',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM shopping.products p),
 'lists',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM shopping.lists l),
 'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM shopping.items i),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM core.audit_log a))::text`
	if err := f.owner.QueryRow(f.ctx, query).Scan(&before); err != nil {
		t.Fatal(err)
	}
	barcodeRestoreShopping7(t, f)
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("migrated barcode table/audit contract not ready", err)
	}
	if err := f.owner.QueryRow(f.ctx, query).Scan(&after); err != nil || before != after {
		t.Fatal("forward migration changed existing catalogue, items or audit", err)
	}
	var aliases int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.barcodes").Scan(&aliases); err != nil || aliases != 0 {
		t.Fatal("upgrade fabricated barcode assignments", aliases, err)
	}
}

func TestShoppingBarcodeReadyRejectsCatalogGuardAndPolicyDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, test := range []struct{ name, damage, repair string }{
		{"capacity guard", "ALTER TABLE shopping.barcodes DISABLE TRIGGER barcode_identity", "ALTER TABLE shopping.barcodes ENABLE TRIGGER barcode_identity"},
		{"capacity guard volatility", "ALTER FUNCTION shopping.guard_barcode() STABLE", "ALTER FUNCTION shopping.guard_barcode() VOLATILE"},
		{"pack guard", "ALTER TABLE shopping.products DISABLE TRIGGER barcode_product_pack", "ALTER TABLE shopping.products ENABLE TRIGGER barcode_product_pack"},
		{"pack guard volatility", "ALTER FUNCTION shopping.guard_barcode_pack() STABLE", "ALTER FUNCTION shopping.guard_barcode_pack() VOLATILE"},
		{"guard direct execution", "GRANT EXECUTE ON FUNCTION shopping.guard_barcode() TO household_app", "REVOKE EXECUTE ON FUNCTION shopping.guard_barcode() FROM household_app"},
		{"lookup index", "DROP INDEX shopping.barcodes_product", "CREATE INDEX barcodes_product ON shopping.barcodes(household_id,product_id)"},
		{"version default", "ALTER TABLE shopping.barcodes ALTER COLUMN version SET DEFAULT 2", "ALTER TABLE shopping.barcodes ALTER COLUMN version SET DEFAULT 1"},
		{"audit policy", "UPDATE core.audit_policies SET safe_columns=ARRAY['product_id','version','code'] WHERE schema_name='shopping' AND table_name='barcodes'", "UPDATE core.audit_policies SET safe_columns=ARRAY['product_id','version'] WHERE schema_name='shopping' AND table_name='barcodes'"},
		{"runtime identity rewrite", "GRANT UPDATE ON shopping.barcodes TO household_app", "REVOKE UPDATE ON shopping.barcodes FROM household_app; GRANT UPDATE(id) ON shopping.barcodes TO household_app"},
		{"missing alias row lock permission", "REVOKE UPDATE(id) ON shopping.barcodes FROM household_app", "GRANT UPDATE(id) ON shopping.barcodes TO household_app"},
		{"extra alias column update", "GRANT UPDATE(code) ON shopping.barcodes TO household_app", "REVOKE UPDATE(code) ON shopping.barcodes FROM household_app"},
		{"alias row lock grant option", "GRANT UPDATE(id) ON shopping.barcodes TO household_app WITH GRANT OPTION", "REVOKE GRANT OPTION FOR UPDATE(id) ON shopping.barcodes FROM household_app"},
		{"public alias row lock permission", "GRANT UPDATE(id) ON shopping.barcodes TO PUBLIC", "REVOKE UPDATE(id) ON shopping.barcodes FROM PUBLIC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, test.damage); err != nil {
				t.Fatal(err)
			}
			if err := database.Ready(f.ctx, f.pool); err == nil {
				t.Fatal("incomplete barcode contract passed Ready")
			}
			if _, err := f.owner.Exec(f.ctx, test.repair); err != nil {
				t.Fatal(err)
			}
			if err := database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal("exact repaired contract not ready", err)
			}
		})
	}
}

func TestShoppingBarcodeUnsupportedProducerCoreRefusesAtomicUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	barcodeRestoreShopping7(t, f)
	var actualVersion int
	if err := f.owner.QueryRow(f.ctx, "SELECT version FROM core.schema_migrations").Scan(&actualVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=27"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "database", "migrations", "shopping", "000008_local_barcodes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := f.owner.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, failure := connection.Exec(f.ctx, string(migration))
	_, rollbackErr := connection.Exec(f.ctx, "ROLLBACK")
	connection.Release()
	if failure == nil || !strings.Contains(failure.Error(), "intermediate producer schemas are unsupported") || rollbackErr != nil {
		t.Fatal("intermediate producer core was overwritten", failure, rollbackErr)
	}
	var absent bool
	if err = f.owner.QueryRow(f.ctx, "SELECT to_regclass('shopping.barcodes') IS NULL AND NOT EXISTS(SELECT 1 FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes')").Scan(&absent); err != nil || !absent {
		t.Fatal("unsupported upgrade left partial table/policy", absent, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1", actualVersion); err != nil {
		t.Fatal(err)
	}
}

func TestShoppingBarcodeNewerCoreRequiresExactPrivateAuditMarker(t *testing.T) {
	f := newIntegrationFixture(t)
	barcodeRestoreShopping7(t, f)
	var actualVersion int
	var markerExists bool
	var originalMarker string
	if err := f.owner.QueryRow(f.ctx, "SELECT version,to_regprocedure('core.shopping_barcode_audit_supported()') IS NOT NULL FROM core.schema_migrations").Scan(&actualVersion, &markerExists); err != nil {
		t.Fatal(err)
	}
	if markerExists {
		if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('core.shopping_barcode_audit_supported()'::regprocedure)").Scan(&originalMarker); err != nil {
			t.Fatal(err)
		}
		if _, err := f.owner.Exec(f.ctx, "ALTER FUNCTION core.shopping_barcode_audit_supported() RENAME TO barcode_fixture_saved_marker"); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = f.owner.Exec(cleanup, "DROP FUNCTION IF EXISTS core.shopping_barcode_audit_supported()")
		if markerExists {
			if _, err := f.owner.Exec(cleanup, "ALTER FUNCTION core.barcode_fixture_saved_marker() RENAME TO shopping_barcode_audit_supported"); err != nil {
				t.Error("restore actual private marker", err)
			}
		}
		if _, err := f.owner.Exec(cleanup, "UPDATE core.schema_migrations SET version=$1", actualVersion); err != nil {
			t.Error("restore actual core version", err)
		}
	}()
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=33"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "database", "migrations", "shopping", "000008_local_barcodes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var originalAudit string
	if err = f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('core.audit_ready()'::regprocedure)").Scan(&originalAudit); err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{"missing marker", "wrong marker body", "public marker"} {
		t.Run(damage, func(t *testing.T) {
			if damage != "missing marker" {
				definition := "CREATE FUNCTION core.shopping_barcode_audit_supported() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$ SELECT true $$"
				// Core26 has no genuine shape helper. Its public-marker case
				// rejects both the body and ACL; actual newer-core runs reuse
				// the real definition and isolate the inappropriate grant.
				if damage == "public marker" && markerExists {
					definition = originalMarker
				}
				if _, err := f.owner.Exec(f.ctx, definition); err != nil {
					t.Fatal(err)
				}
				if damage == "wrong marker body" {
					if _, err := f.owner.Exec(f.ctx, "REVOKE ALL ON FUNCTION core.shopping_barcode_audit_supported() FROM PUBLIC,household_app"); err != nil {
						t.Fatal(err)
					}
				} else if _, err := f.owner.Exec(f.ctx, "GRANT EXECUTE ON FUNCTION core.shopping_barcode_audit_supported() TO PUBLIC,household_app"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := f.owner.Exec(cleanup, "DROP FUNCTION core.shopping_barcode_audit_supported()"); err != nil {
						t.Error("remove deliberately malformed marker", err)
					}
				}()
			}
			connection, err := f.owner.Acquire(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, failure := connection.Exec(f.ctx, string(migration))
			_, rollbackErr := connection.Exec(f.ctx, "ROLLBACK")
			connection.Release()
			if failure == nil || !strings.Contains(failure.Error(), "approved local barcode audit support") || rollbackErr != nil {
				t.Fatal("newer core version alone or malformed marker was accepted", failure, rollbackErr)
			}
			var absent bool
			var afterAudit string
			if err := f.owner.QueryRow(f.ctx, "SELECT to_regclass('shopping.barcodes') IS NULL AND NOT EXISTS(SELECT 1 FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes'),pg_get_functiondef('core.audit_ready()'::regprocedure)").Scan(&absent, &afterAudit); err != nil || !absent || afterAudit != originalAudit {
				t.Fatal("refused malformed marker left partial barcode schema or rewrote current audit readiness", absent, err)
			}
		})
	}
}
