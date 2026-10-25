//go:build integration

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type purchaseSchemaLine struct {
	RequirementID string          `json:"requirement_id"`
	Version       string          `json:"requirement_version"`
	ComponentID   string          `json:"component_id"`
	IngredientID  string          `json:"ingredient_id"`
	Changed       bool            `json:"changed"`
	Action        string          `json:"item_action"`
	Before        json.RawMessage `json:"requirement_before"`
	After         json.RawMessage `json:"requirement_after"`
	ItemBefore    json.RawMessage `json:"item_before"`
	ItemAfter     json.RawMessage `json:"item_after"`
}

type purchaseSchemaCounts struct{ sources, changes, creates, updates, removes int }

func purchaseSchemaTx(t *testing.T, f *integrationFixture) pgx.Tx {
	t.Helper()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.app.Store.BeginMutation(ctx, f.identity, "mealplan", f.app.Store.States()["mealplan"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	return tx
}

func purchaseSchemaProof(t *testing.T, f *integrationFixture, tx pgx.Tx, op, receipt, kind string, counts purchaseSchemaCounts) {
	t.Helper()
	hash := sha256.Sum256([]byte("Household planning purchase schema fixture"))
	_, err := tx.Exec(f.ctx, `INSERT INTO core.planning_purchase_operations(id,household_id,created_by,request_key,request_hash,scope_hash,
 mealplan_generation,pantry_generation,shopping_generation,receipt_id,kind,source_count,change_count,created_count,updated_count,removed_count)
 VALUES($1,$2,$3,$4,$5,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, op, f.identity.HouseholdID, f.identity.UserID, value.UUID(), hash[:],
		f.app.Store.States()["mealplan"].Generation, f.app.Store.States()["pantry"].Generation, f.app.Store.States()["shopping"].Generation,
		receipt, kind, counts.sources, counts.changes, counts.creates, counts.updates, counts.removes)
	if err != nil {
		t.Fatal("operation fixture", err)
	}
}

func purchaseSchemaRead(t *testing.T, f *integrationFixture, tx pgx.Tx, id string) json.RawMessage {
	t.Helper()
	var raw json.RawMessage
	if err := tx.QueryRow(f.ctx, "SELECT to_jsonb(r) FROM core.planning_purchase_requirements r WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func purchaseSchemaSource(t *testing.T, f *integrationFixture, tx pgx.Tx, id string, before json.RawMessage, changed bool) purchaseSchemaLine {
	t.Helper()
	out := purchaseSchemaLine{RequirementID: id, Before: before, After: purchaseSchemaRead(t, f, tx, id), Changed: changed, Action: "none"}
	if err := tx.QueryRow(f.ctx, "SELECT component_id,ingredient_id,version::text FROM core.planning_purchase_requirements WHERE id=$1", id).Scan(&out.ComponentID, &out.IngredientID, &out.Version); err != nil {
		t.Fatal(err)
	}
	return out
}

func purchaseSchemaReceipt(t *testing.T, f *integrationFixture, tx pgx.Tx, op string, sources []purchaseSchemaLine) {
	t.Helper()
	raw, err := json.Marshal(store.Record{"sources": sources})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `INSERT INTO shopping.planning_purchase_receipts(id,household_id,operation_id,request_hash,scope_hash,
 mealplan_generation,pantry_generation,shopping_generation,snapshot,created_by)
 SELECT receipt_id,household_id,id,request_hash,scope_hash,mealplan_generation,pantry_generation,shopping_generation,$2,created_by
 FROM core.planning_purchase_operations WHERE id=$1`, op, raw)
	if err != nil {
		t.Fatal("receipt fixture", err)
	}
}

func purchaseSchemaRejected(t *testing.T, err error) {
	t.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" {
		t.Fatal("expected a real contract constraint failure", err)
	}
}

func purchaseSchemaRegister(t *testing.T) (*integrationFixture, []string, string) {
	t.Helper()
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	// Demand is1.2kg. Make these zero-purchase requirements genuinely covered.
	var lot string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.lots WHERE product_id=$1", product["id"]).Scan(&lot); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot, "quantity_delta": "1", "reason": "receive"})
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}})
	if err != nil || !view.Ready || len(view.Lines) != 2 {
		t.Fatal("actual reviewed requirement basis", view, err)
	}
	bases := make(map[string]string, len(view.Lines))
	for _, line := range view.Lines {
		bases[line.IngredientID] = line.Basis
	}
	ids := []string{value.UUID(), value.UUID()}
	op, receipt := value.UUID(), value.UUID()
	tx := purchaseSchemaTx(t, f)
	purchaseSchemaProof(t, f, tx, op, receipt, "sync", purchaseSchemaCounts{sources: 2, changes: 2})
	var bindings []struct{ id, ingredient string }
	rows, err := tx.Query(f.ctx, "SELECT id,ingredient_id FROM core.planning_ingredient_bindings WHERE component_id=$1 ORDER BY ingredient_id", cid)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var binding struct{ id, ingredient string }
		if err = rows.Scan(&binding.id, &binding.ingredient); err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, binding)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(bindings) != 2 {
		t.Fatal("complete binding fixture", bindings, err)
	}
	lines := make([]purchaseSchemaLine, 0, 2)
	for i, binding := range bindings {
		purchaseSchemaInsertRequirement(t, f, tx, ids[i], binding.id, op, bases[binding.ingredient])
		lines = append(lines, purchaseSchemaSource(t, f, tx, ids[i], nil, true))
	}
	purchaseSchemaReceipt(t, f, tx, op, lines)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("complete whole-meal registration", err)
	}
	return f, ids, cid
}

func purchaseSchemaInsertRequirement(t *testing.T, f *integrationFixture, tx pgx.Tx, id, binding, op string, reviewed ...string) {
	t.Helper()
	var basis []byte
	if len(reviewed) == 1 {
		var err error
		basis, err = hex.DecodeString(reviewed[0])
		if err != nil || len(basis) != sha256.Size {
			t.Fatal("invalid reviewed basis fixture", err)
		}
	}
	_, err := tx.Exec(f.ctx, `INSERT INTO core.planning_purchase_requirements(id,household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,
 source_hash,reviewed_basis_hash,binding_id,binding_version,product_id,pantry_generation,unit_code,last_sync_operation_id,created_by,changed_by)
 SELECT $1,household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,source_hash,coalesce($4::bytea,source_hash),id,version,product_id,pantry_generation,unit_code,$3,created_by,changed_by
 FROM core.planning_ingredient_bindings WHERE id=$2`, id, binding, op, basis)
	if err != nil {
		t.Fatal("zero-purchase requirement fixture", err)
	}
}

func TestPlanningPurchaseProofCompletenessAndWholeMealSchema(t *testing.T) {
	f, ids, cid := purchaseSchemaRegister(t)
	t.Run("unchanged new key creates evidence without changing requirements", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		op := value.UUID()
		purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 2})
		lines := make([]purchaseSchemaLine, 0, 2)
		for _, id := range ids {
			before := purchaseSchemaRead(t, f, tx, id)
			lines = append(lines, purchaseSchemaSource(t, f, tx, id, before, false))
		}
		purchaseSchemaReceipt(t, f, tx, op, lines)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal("valid zero-effect proof", err)
		}
	})
	t.Run("proof only rolls back", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		op := value.UUID()
		purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{})
		purchaseSchemaRejected(t, tx.Commit(f.ctx))
		var exists bool
		if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.planning_purchase_operations WHERE id=$1)", op).Scan(&exists); err != nil || exists {
			t.Fatal("failed proof survived rollback", exists, err)
		}
	})
	t.Run("claiming a create without an item fails atomically", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		before := purchaseSchemaRead(t, f, tx, ids[0])
		op := value.UUID()
		purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 2, changes: 1, creates: 1})
		if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET last_sync_operation_id=$2 WHERE id=$1", ids[0], op); err != nil {
			t.Fatal(err)
		}
		line := purchaseSchemaSource(t, f, tx, ids[0], before, true)
		line.Action = "create"
		line.ItemAfter = json.RawMessage(`{"id":"` + value.UUID() + `"}`)
		unchanged := purchaseSchemaRead(t, f, tx, ids[1])
		purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{line, purchaseSchemaSource(t, f, tx, ids[1], unchanged, false)})
		purchaseSchemaRejected(t, tx.Commit(f.ctx))
		var version int64
		if err := f.pool.QueryRow(f.ctx, "SELECT version FROM core.planning_purchase_requirements WHERE id=$1", ids[0]).Scan(&version); err != nil || version != 1 {
			t.Fatal("late failure changed current requirement", version, err)
		}
	})
	t.Run("old proof cannot end or reactivate a meal", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET active=false,procurement_disposition='detached' WHERE id=ANY($1::uuid[])", ids); err != nil {
			t.Fatal(err)
		}
		purchaseSchemaRejected(t, tx.Commit(f.ctx))
	})
	t.Run("repeatable read scope registration is forbidden", func(t *testing.T) {
		rr, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rr.Rollback(f.ctx) }()
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
		if err = audit.Bind(ctx, rr); err != nil {
			t.Fatal(err)
		}
		_, err = rr.Exec(f.ctx, `INSERT INTO core.planning_purchase_operations(household_id,created_by,request_key,request_hash,scope_hash,mealplan_generation,pantry_generation,shopping_generation,receipt_id,kind,source_count,change_count,created_count,updated_count,removed_count)
 VALUES($1,$2,$3,decode(repeat('01',32),'hex'),decode(repeat('02',32),'hex'),1,1,1,$4,'sync',0,0,0,0,0)`, f.identity.HouseholdID, f.identity.UserID, value.UUID(), value.UUID())
		purchaseSchemaRejected(t, err)
	})
	t.Run("ordinary deletion retains blocked active source identity", func(t *testing.T) {
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
		if _, err := audit.Exec(ctx, f.pool, "DELETE FROM mealplan.components WHERE id=$1", cid); err != nil {
			t.Fatal(err)
		}
		var blocked, bindings int
		if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE active AND review_reason='source_missing'),
 (SELECT count(*) FROM core.planning_ingredient_bindings WHERE component_id=$1)`, cid).Scan(&blocked, &bindings); err != nil || blocked != 2 || bindings != 0 {
			t.Fatal("deleted source disappeared from scope or retained binding payload", blocked, bindings, err)
		}
	})
	t.Run("explicit whole-meal end works after source deletion", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		op := value.UUID()
		purchaseSchemaProof(t, f, tx, op, value.UUID(), "end", purchaseSchemaCounts{sources: 2, changes: 2})
		lines := make([]purchaseSchemaLine, 0, 2)
		for _, id := range ids {
			before := purchaseSchemaRead(t, f, tx, id)
			if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET active=false,review_reason='',procurement_disposition='detached',last_sync_operation_id=$2 WHERE id=$1", id, op); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, purchaseSchemaSource(t, f, tx, id, before, true))
		}
		purchaseSchemaReceipt(t, f, tx, op, lines)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal("ending must not require the deleted source", err)
		}
	})
}

func purchaseSchemaItem(t *testing.T, f *integrationFixture, tx pgx.Tx, id string) (json.RawMessage, []byte, int64) {
	t.Helper()
	var raw json.RawMessage
	var hash []byte
	var version int64
	if err := tx.QueryRow(f.ctx, "SELECT to_jsonb(i),sha256(convert_to(to_jsonb(i)::text,'UTF8')),version FROM shopping.items i WHERE id=$1", id).Scan(&raw, &hash, &version); err != nil {
		t.Fatal(err)
	}
	return raw, hash, version
}

func purchaseSchemaUnchanged(t *testing.T, f *integrationFixture, tx pgx.Tx, id string) purchaseSchemaLine {
	t.Helper()
	before := purchaseSchemaRead(t, f, tx, id)
	return purchaseSchemaSource(t, f, tx, id, before, false)
}

func purchaseSchemaManage(t *testing.T, f *integrationFixture, ids []string) (string, string) {
	t.Helper()
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Managed planning purchase"})
	itemID := value.UUID()
	tx := purchaseSchemaTx(t, f)
	before := purchaseSchemaRead(t, f, tx, ids[0])
	op := value.UUID()
	purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 2, changes: 1, creates: 1})
	if _, err := tx.Exec(f.ctx, `INSERT INTO shopping.items(id,household_id,list_id,name,quantity,unit_code,created_by)
 VALUES($1,$2,$3,'Explicit managed ingredient',0.2,'kg',$4)`, itemID, f.identity.HouseholdID, list["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	item, hash, version := purchaseSchemaItem(t, f, tx, itemID)
	if _, err := tx.Exec(f.ctx, `UPDATE core.planning_purchase_requirements SET procurement_disposition='managed',shopping_generation=$2,
 list_id=$3,item_id=$4,item_version=$5,item_fingerprint=$6,pending_quantity=0.2,last_sync_operation_id=$7 WHERE id=$1`, ids[0],
		f.app.Store.States()["shopping"].Generation, list["id"], itemID, version, hash, op); err != nil {
		t.Fatal(err)
	}
	line := purchaseSchemaSource(t, f, tx, ids[0], before, true)
	line.Action, line.ItemAfter = "create", item
	purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{line, purchaseSchemaUnchanged(t, f, tx, ids[1])})
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return store.String(list["id"]), itemID
}

func TestPlanningPurchaseItemEffectAndCheckoutEvidence(t *testing.T) {
	f, ids, _ := purchaseSchemaRegister(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Planning schema effect evidence"})
	itemID := value.UUID()
	tx := purchaseSchemaTx(t, f)
	before := purchaseSchemaRead(t, f, tx, ids[0])
	op := value.UUID()
	purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 2, changes: 1, creates: 1})
	if _, err := tx.Exec(f.ctx, `INSERT INTO shopping.items(id,household_id,list_id,name,quantity,unit_code,note,created_by)
 VALUES($1,$2,$3,'Planning exact purchase',0.2,'kg','Private original audit sentinel',$4)`, itemID, f.identity.HouseholdID, list["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	item, hash, version := purchaseSchemaItem(t, f, tx, itemID)
	_, err := tx.Exec(f.ctx, `UPDATE core.planning_purchase_requirements SET procurement_disposition='managed',shopping_generation=$2,
 list_id=$3,item_id=$4,item_version=$5,item_fingerprint=$6,pending_quantity=0.2,last_sync_operation_id=$7 WHERE id=$1`,
		ids[0], f.app.Store.States()["shopping"].Generation, list["id"], itemID, version, hash, op)
	if err != nil {
		t.Fatal(err)
	}
	line := purchaseSchemaSource(t, f, tx, ids[0], before, true)
	line.Action, line.ItemAfter = "create", item
	purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{line, purchaseSchemaUnchanged(t, f, tx, ids[1])})
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("actual same-transaction purchase create", err)
	}
	t.Run("old operation cannot authorize an otherwise valid item update", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		if _, err := tx.Exec(f.ctx, "UPDATE shopping.items SET quantity=0.3,version=version+1,updated_at=clock_timestamp() WHERE id=$1", itemID); err != nil {
			t.Fatal(err)
		}
		_, hash, version := purchaseSchemaItem(t, f, tx, itemID)
		if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET pending_quantity=0.3,item_version=$2,item_fingerprint=$3 WHERE id=$1", ids[0], version, hash); err != nil {
			t.Fatal(err)
		}
		purchaseSchemaRejected(t, tx.Commit(f.ctx))
	})
	t.Run("new proof cannot fabricate checkout references", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		before := purchaseSchemaRead(t, f, tx, ids[0])
		op := value.UUID()
		purchaseSchemaProof(t, f, tx, op, value.UUID(), "checkout", purchaseSchemaCounts{sources: 2, changes: 1})
		item, _, _ := purchaseSchemaItem(t, f, tx, itemID)
		_, err := tx.Exec(f.ctx, `UPDATE core.planning_purchase_requirements SET procurement_disposition='stored',
 shopping_generation=NULL,list_id=NULL,item_id=NULL,item_version=NULL,item_fingerprint=NULL,pending_quantity=0,
 last_checkout_generation=$2,last_checkout_id=$3,last_checkout_item_id=$4,last_sync_operation_id=$5 WHERE id=$1`,
			ids[0], f.app.Store.States()["shopping"].Generation, value.UUID(), itemID, op)
		if err != nil {
			t.Fatal(err)
		}
		line := purchaseSchemaSource(t, f, tx, ids[0], before, true)
		line.ItemBefore, line.ItemAfter = item, item
		purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{line, purchaseSchemaUnchanged(t, f, tx, ids[1])})
		purchaseSchemaRejected(t, tx.Commit(f.ctx))
	})
	for _, tc := range []struct {
		name   string
		tamper bool
	}{{"invented previous note is rejected despite matching id and version", true}, {"exact previous row and actual update commit", false}} {
		t.Run(tc.name, func(t *testing.T) {
			tx := purchaseSchemaTx(t, f)
			before := purchaseSchemaRead(t, f, tx, ids[0])
			itemBefore, _, _ := purchaseSchemaItem(t, f, tx, itemID)
			op := value.UUID()
			purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 2, changes: 1, updates: 1})
			if _, err := tx.Exec(f.ctx, "UPDATE shopping.items SET quantity=0.3,note='Private changed audit sentinel',version=version+1,updated_at=clock_timestamp() WHERE id=$1", itemID); err != nil {
				t.Fatal(err)
			}
			itemAfter, hash, version := purchaseSchemaItem(t, f, tx, itemID)
			if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET pending_quantity=0.3,item_version=$2,item_fingerprint=$3,last_sync_operation_id=$4 WHERE id=$1", ids[0], version, hash, op); err != nil {
				t.Fatal(err)
			}
			if tc.tamper {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(itemBefore, &fields); err != nil {
					t.Fatal(err)
				}
				fields["note"] = json.RawMessage(`"Invented previous note"`)
				var err error
				itemBefore, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
			}
			line := purchaseSchemaSource(t, f, tx, ids[0], before, true)
			line.Action, line.ItemBefore, line.ItemAfter = "update", itemBefore, itemAfter
			purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{line, purchaseSchemaUnchanged(t, f, tx, ids[1])})
			err := tx.Commit(f.ctx)
			if tc.tamper {
				purchaseSchemaRejected(t, err)
			} else if err != nil {
				t.Fatal("real update with exact evidence", err)
			}
		})
	}
	t.Run("unreported real item effect rolls back", func(t *testing.T) {
		tx := purchaseSchemaTx(t, f)
		op := value.UUID()
		purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 2})
		if _, err := tx.Exec(f.ctx, "INSERT INTO shopping.items(household_id,list_id,name,quantity,unit_code,created_by) VALUES($1,$2,'Unreported effect',1,'kg',$3)", f.identity.HouseholdID, list["id"], f.identity.UserID); err != nil {
			t.Fatal(err)
		}
		item, _, _ := purchaseSchemaItem(t, f, tx, itemID)
		line := purchaseSchemaUnchanged(t, f, tx, ids[0])
		line.ItemBefore, line.ItemAfter = item, item
		purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{line, purchaseSchemaUnchanged(t, f, tx, ids[1])})
		purchaseSchemaRejected(t, tx.Commit(f.ctx))
	})
	var rawAudit string
	if err := f.pool.QueryRow(f.ctx, "SELECT string_agg(changes::text||details::text,'') FROM core.audit_log WHERE module_id='shopping' AND resource_type='items' AND resource_id=$1", itemID).Scan(&rawAudit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawAudit, "Private original audit sentinel") || strings.Contains(rawAudit, "Private changed audit sentinel") || !strings.Contains(rawAudit, "row_before_sha256") {
		t.Fatal("exact evidence disclosed private note or omitted fingerprints")
	}
}

func TestPlanningPurchaseWholeMealSourceReplacement(t *testing.T) {
	f, ids, cid := purchaseSchemaRegister(t)
	var pid, mid, productID string
	if err := f.pool.QueryRow(f.ctx, "SELECT plan_id,meal_id,product_id FROM core.planning_purchase_requirements WHERE id=$1", ids[0]).Scan(&pid, &mid, &productID); err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	replacement := value.UUID()
	if _, err := audit.Exec(ctx, f.pool, `INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 SELECT $1,household_id,meal_id,1,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by
 FROM mealplan.components WHERE id=$2`, replacement, cid); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Exec(ctx, f.pool, "DELETE FROM mealplan.components WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	bindPlanningIngredients(t, f, pid, mid, replacement, store.Record{"id": productID})
	tx := purchaseSchemaTx(t, f)
	op := value.UUID()
	purchaseSchemaProof(t, f, tx, op, value.UUID(), "sync", purchaseSchemaCounts{sources: 4, changes: 4})
	lines := make([]purchaseSchemaLine, 0, 4)
	for _, id := range ids {
		before := purchaseSchemaRead(t, f, tx, id)
		if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET active=false,review_reason='',procurement_disposition='detached',last_sync_operation_id=$2 WHERE id=$1", id, op); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, purchaseSchemaSource(t, f, tx, id, before, true))
	}
	var bindings []string
	if err := tx.QueryRow(f.ctx, "SELECT ARRAY(SELECT id::text FROM core.planning_ingredient_bindings WHERE component_id=$1 ORDER BY ingredient_id)", replacement).Scan(&bindings); err != nil || len(bindings) != 2 {
		t.Fatal("replacement must contain the whole frozen source", bindings, err)
	}
	for _, binding := range bindings {
		id := value.UUID()
		purchaseSchemaInsertRequirement(t, f, tx, id, binding, op)
		lines = append(lines, purchaseSchemaSource(t, f, tx, id, nil, true))
	}
	purchaseSchemaReceipt(t, f, tx, op, lines)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal("deferred whole-meal validation must use the final active source set", err)
	}
	var active, ended int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FILTER(WHERE active AND component_id=$1),count(*) FILTER(WHERE NOT active AND component_id=$2)
 FROM core.planning_purchase_requirements WHERE meal_id=$3`, replacement, cid, mid).Scan(&active, &ended); err != nil || active != 2 || ended != 2 {
		t.Fatal("replacement changed or lost whole-meal scope", active, ended, err)
	}
	// A new proof cannot authorize ending just one ingredient of that meal.
	tx = purchaseSchemaTx(t, f)
	op = value.UUID()
	purchaseSchemaProof(t, f, tx, op, value.UUID(), "end", purchaseSchemaCounts{sources: 1, changes: 1})
	id := lines[2].RequirementID
	before := purchaseSchemaRead(t, f, tx, id)
	if _, err := tx.Exec(f.ctx, "UPDATE core.planning_purchase_requirements SET active=false,procurement_disposition='detached',last_sync_operation_id=$2 WHERE id=$1", id, op); err != nil {
		t.Fatal(err)
	}
	purchaseSchemaReceipt(t, f, tx, op, []purchaseSchemaLine{purchaseSchemaSource(t, f, tx, id, before, true)})
	purchaseSchemaRejected(t, tx.Commit(f.ctx))
}

func TestPlanningPurchaseReadinessDetectsContractDrift(t *testing.T) {
	f, _, _ := purchaseSchemaRegister(t)
	for _, tc := range []struct{ name, breakQuery, repairQuery string }{
		{"proof select", "REVOKE SELECT ON core.planning_purchase_operations FROM household_app", "GRANT SELECT ON core.planning_purchase_operations TO household_app"},
		{"proof insert", "REVOKE INSERT ON core.planning_purchase_operations FROM household_app", "GRANT INSERT ON core.planning_purchase_operations TO household_app"},
		{"requirement select", "REVOKE SELECT ON core.planning_purchase_requirements FROM household_app", "GRANT SELECT ON core.planning_purchase_requirements TO household_app"},
		{"requirement insert", "REVOKE INSERT ON core.planning_purchase_requirements FROM household_app", "GRANT INSERT ON core.planning_purchase_requirements TO household_app"},
		{"requirement update", "REVOKE UPDATE ON core.planning_purchase_requirements FROM household_app", "GRANT UPDATE ON core.planning_purchase_requirements TO household_app"},
		{"receipt select", "REVOKE SELECT ON shopping.planning_purchase_receipts FROM household_app", "GRANT SELECT ON shopping.planning_purchase_receipts TO household_app"},
		{"receipt insert", "REVOKE INSERT ON shopping.planning_purchase_receipts FROM household_app", "GRANT INSERT ON shopping.planning_purchase_receipts TO household_app"},
		{"forbidden proof edit", "GRANT UPDATE ON core.planning_purchase_operations TO household_app", "REVOKE UPDATE ON core.planning_purchase_operations FROM household_app"},
		{"forbidden requirement delete", "GRANT DELETE ON core.planning_purchase_requirements TO household_app", "REVOKE DELETE ON core.planning_purchase_requirements FROM household_app"},
		{"forbidden receipt delete", "GRANT DELETE ON shopping.planning_purchase_receipts TO household_app", "REVOKE DELETE ON shopping.planning_purchase_receipts FROM household_app"},
		{"disabled evidence", "ALTER TABLE core.planning_purchase_requirements DISABLE TRIGGER planning_purchase_requirement_evidence", "ALTER TABLE core.planning_purchase_requirements ENABLE TRIGGER planning_purchase_requirement_evidence"},
		{"wrong request uniqueness", "ALTER TABLE core.planning_purchase_operations DROP CONSTRAINT planning_purchase_request,ADD CONSTRAINT planning_purchase_request UNIQUE(household_id,request_key)", "ALTER TABLE core.planning_purchase_operations DROP CONSTRAINT planning_purchase_request,ADD CONSTRAINT planning_purchase_request UNIQUE(household_id,created_by,request_key)"},
		{"wrong item index", "DROP INDEX core.planning_purchase_item; CREATE UNIQUE INDEX planning_purchase_item ON core.planning_purchase_requirements(household_id,item_id,meal_id) WHERE item_id IS NOT NULL", "DROP INDEX core.planning_purchase_item; CREATE UNIQUE INDEX planning_purchase_item ON core.planning_purchase_requirements(household_id,item_id) WHERE item_id IS NOT NULL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, tc.breakQuery); err != nil {
				t.Fatal(err)
			}
			readErr := database.Ready(f.ctx, f.pool)
			if _, err := f.owner.Exec(f.ctx, tc.repairQuery); err != nil {
				t.Fatal(err)
			}
			if readErr == nil {
				t.Fatal("readiness accepted changed purchase contract")
			}
			if err := database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal("repair did not restore readiness", err)
			}
		})
	}
}
