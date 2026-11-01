//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/value"
)

func financeAuthorityMember(t *testing.T, f *recipeExpansionFixture, role string) auth.Identity {
	t.Helper()
	user, member := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.users(id,username,display_name,password_hash,must_change_password)
 SELECT $1,$2,'Synthetic authority fixture',password_hash,false FROM core.users WHERE id=$3`, user, "finance-authority-"+user, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.memberships(id,household_id,user_id,role) VALUES($1,$2,$3,$4)`, member, f.identity.HouseholdID, user, role); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(f.app, config.Config{EncryptionKey: auth.RandomToken(), RateSecret: auth.RandomToken(),
		SessionIdle: time.Hour, SessionAbsolute: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	_, identity, err := a.Login(f.ctx, "finance-authority-"+user, recipeExpansionPassword, "", "127.0.0.1", "finance authority fixture")
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func financeAuthorityInitialAccount(t *testing.T, f *recipeExpansionFixture, actor auth.Identity, grants []FinanceInitialGrant) string {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := readFinancialScope(f.ctx, tx, actor)
	if err != nil {
		rollback(tx)
		t.Fatal(err)
	}
	stamp, err := financeProspectiveAccess(f.ctx, tx, actor, scope, nil, "view")
	rollback(tx)
	if err != nil {
		t.Fatal(err)
	}
	rid := value.UUID()
	payload, err := json.Marshal(Record{"name": "private-account-authority-sentinel", "account_type": "bank", "currency": "EUR", "exponent": 2, "notes": "never shared first"})
	if err != nil {
		t.Fatal(err)
	}
	responsible := actor.MembershipID
	command := FinanceCommand{Resource: "accounts", Action: "save", ID: rid, Generation: scope.generation,
		AccessVersion: stamp, Payload: payload, InitialAccess: &FinanceInitialAccess{AccessMode: "selected", ResponsibleMembershipID: &responsible, Grants: grants}}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, actor, key, value.UUID(), command)
	if err != nil {
		t.Fatal("atomic selected preview", err)
	}
	command.ReviewHash = String(preview["impact_hash"])
	if _, err = f.store.FinanceApply(f.ctx, actor, key, value.UUID(), command); err != nil {
		t.Fatal("atomic selected apply", err)
	}
	return rid
}

func TestFinanceAuthorityInitialSelectedBeforeFirstAudit(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	reader := financeAuthorityMember(t, f, "reader")
	rid := financeAuthorityInitialAccount(t, f, f.identity, []FinanceInitialGrant{
		{MembershipID: f.identity.MembershipID, Permission: "edit"}, {MembershipID: reader.MembershipID, Permission: "view"},
	})
	var sharedCreation, selectedCreation int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FILTER(WHERE a.changes->'access_mode'->>'after'='household'),
 count(*) FILTER(WHERE a.changes->'access_mode'->>'after'='selected') FROM core.audit_log a JOIN core.financial_policies p ON p.id=a.resource_id
 WHERE a.household_id=$1 AND a.module_id='core' AND a.resource_type='financial_policies' AND a.action='CREATE' AND p.resource_type='accounts' AND p.resource_id=$2`,
		f.identity.HouseholdID, rid).Scan(&sharedCreation, &selectedCreation); err != nil || sharedCreation != 0 || selectedCreation != 1 {
		t.Fatal("initial private policy was not installed before its only CREATE audit", sharedCreation, selectedCreation, err)
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	scope, err := readFinancialScope(f.ctx, tx, reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = financeResourceAccess(f.ctx, tx, reader, scope, "accounts", rid, "view"); err != nil {
		t.Fatal("explicit Reader view grant", err)
	}
	if _, err = financeResourceAccess(f.ctx, tx, reader, scope, "accounts", rid, "edit"); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatal("Reader obtained financial edit capability", err)
	}
}

func TestFinanceAuthorityAccountIntersectionAndStableIntentStamp(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	other := financeAuthorityMember(t, f, "editor")
	account := financeAuthorityInitialAccount(t, f, f.identity, []FinanceInitialGrant{{MembershipID: f.identity.MembershipID, Permission: "edit"}})
	category := financeNativeCategory(t, f)
	entry := value.UUID()
	in := financeNativeEntry(category, "100")
	in.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: 1, NativeMinor: "100"}
	financeNativeApply(t, f, "entries", "save", entry, 0, in)
	if _, err := f.store.FinanceView(f.ctx, other, FinanceQuery{Pane: "entries", ID: entry}); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatal("shared entry bypassed selected account intersection", err)
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	scope, err := readFinancialScope(f.ctx, tx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	view, err := financeResourceAccess(f.ctx, tx, f.identity, scope, "entries", entry, "view")
	if err != nil {
		t.Fatal(err)
	}
	edit, err := financeResourceAccess(f.ctx, tx, f.identity, scope, "entries", entry, "edit")
	if err != nil || view != edit {
		t.Fatal("intent changed unchanged current closure stamp", err)
	}
	var retained bool
	if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.finance_origin_resources o JOIN core.financial_requirement_members m ON m.set_id=o.set_id
 JOIN core.financial_subjects s ON s.id=m.subject_id WHERE o.household_id=$1 AND o.resource_type='entries' AND o.resource_id=$2 AND s.resource_type='accounts' AND s.resource_id=$3)`,
		f.identity.HouseholdID, entry, account).Scan(&retained); err != nil || !retained {
		t.Fatal("last sidecar account was omitted from final parent closure", err)
	}
}

func TestFinanceAuthorityExcludedAdministratorRecoveryIsMetadataOnly(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	owner := financeAuthorityMember(t, f, "editor")
	account := financeAuthorityInitialAccount(t, f, owner, []FinanceInitialGrant{{MembershipID: owner.MembershipID, Permission: "edit"}})
	if _, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "accounts", ID: account}); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatal("administrator bypassed selected content", err)
	}
	metadata, err := f.store.FinanceAccessInfo(f.ctx, f.identity, "accounts", account)
	encoded, marshalErr := json.Marshal(metadata)
	if err != nil || marshalErr != nil || strings.Contains(string(encoded), "sentinel") || metadata["metadata_only"] != true {
		t.Fatal("recovery metadata read private account content", err, marshalErr)
	}
	raw, err := json.Marshal(Record{"target_kind": "accounts", "target_id": account, "policy_version": metadata["policy_version"],
		"new_responsible_membership_id": f.identity.MembershipID, "reason": "Explicit synthetic administrator recovery"})
	if err != nil {
		t.Fatal(err)
	}
	command := FinanceCommand{Resource: "access", Action: "recover", ID: account, Version: 0, Generation: f.store.States()["expenses"].Generation,
		AccessVersion: String(metadata["access_version"]), Payload: raw}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal("real metadata-only recovery preview", err)
	}
	command.ReviewHash = String(preview["impact_hash"])
	if _, err = f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); err != nil {
		t.Fatal("real metadata-only recovery apply", err)
	}
	if _, err = f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "accounts", ID: account}); err != nil {
		t.Fatal("explicit reassignment did not grant the new responsible member its reviewed edit grant", err)
	}
}

func TestFinanceAuthoritySealedKeyRollsBackLateEffects(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	rid := value.UUID()
	command, key, _ := financeNativePreview(t, f, "accounts", "save", rid, 0, Record{"name": "sealed late account", "account_type": "cash", "currency": "EUR", "exponent": 2, "notes": "synthetic"})
	if err := f.store.FinanceSealOperation(f.ctx, f.identity, key, value.UUID(), command.Generation); err != nil {
		t.Fatal("real neutral seal", err)
	}
	if _, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); !errors.Is(err, ErrConflict) {
		t.Fatal("late sealed operation was not rejected", err)
	}
	var facts, proofs, seals int
	if err := f.owner.QueryRow(f.ctx, `SELECT(SELECT count(*) FROM expenses.accounts WHERE household_id=$1 AND id=$2),
 (SELECT count(*) FROM core.finance_operations WHERE household_id=$1 AND created_by=$3 AND request_key=$4),
 (SELECT count(*) FROM core.finance_key_tombstones WHERE household_id=$1 AND created_by=$3 AND request_key=$4)`,
		f.identity.HouseholdID, rid, f.identity.UserID, key).Scan(&facts, &proofs, &seals); err != nil || facts != 0 || proofs != 0 || seals != 1 {
		t.Fatal("sealed late write left a business fact/proof or lost its permanent fence", facts, proofs, seals, err)
	}
}

func TestFinanceAuthorityOwnerDefinerFrameIsNotMaintenance(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	account := financeAuthorityInitialAccount(t, f, f.identity, []FinanceInitialGrant{{MembershipID: f.identity.MembershipID, Permission: "edit"}})
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.test_finance_owner_frame(p_id uuid) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
 BEGIN UPDATE core.financial_policies SET access_mode='household' WHERE resource_type='accounts' AND resource_id=p_id;END $$;
 REVOKE ALL ON FUNCTION core.test_finance_owner_frame(uuid) FROM PUBLIC;GRANT EXECUTE ON FUNCTION core.test_finance_owner_frame(uuid) TO household_app`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.owner.Exec(f.ctx, "DROP FUNCTION IF EXISTS core.test_finance_owner_frame(uuid)") })
	if _, err := f.app.Exec(f.ctx, "SELECT core.test_finance_owner_frame($1)", account); err == nil {
		t.Fatal("App gained maintenance through an owner SECURITY DEFINER frame")
	}
	var mode string
	if err := f.owner.QueryRow(f.ctx, "SELECT access_mode FROM core.financial_policies WHERE household_id=$1 AND resource_type='accounts' AND resource_id=$2", f.identity.HouseholdID, account).Scan(&mode); err != nil || mode != "selected" {
		t.Fatal("rejected owner frame changed private policy", mode, err)
	}
}

func TestFinanceAuthorityLegacyReplaySurvivesResponsePruning(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category, entry := financeNativeCategory(t, f), value.UUID()
	created := financeNativeApply(t, f, "entries", "save", entry, 0, financeNativeEntry(category, "100"))
	m, _ := f.store.Module("expenses")
	e, _ := m.Entity("entries")
	key := value.UUID()
	mutation := Mutation{Version: Int(created["saved_version"]), Generation: f.store.States()["expenses"].Generation,
		Data: map[string]json.RawMessage{"description": json.RawMessage(`"Legacy durable update"`)}}
	first, err := f.store.Write(f.ctx, f.identity, m, e, entry, key, value.UUID(), mutation)
	if err != nil {
		t.Fatal("actual legacy financial update", err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND key=$3", f.identity.HouseholdID, f.identity.UserID, key); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.finance_origin_operations WHERE household_id=$1 AND target_kind='entries' AND target_id=$2", f.identity.HouseholdID, entry).Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.Write(f.ctx, f.identity, m, e, entry, key, value.UUID(), mutation)
	if err != nil || replay["accepted"] != true || replay["replayed"] != true || String(replay["id"]) != entry || Int(replay["accepted_version"]) != Int(first["version"]) {
		t.Fatal("pruned legacy retry did not resolve its minimal permanent proof", replay, err)
	}
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.finance_origin_operations WHERE household_id=$1 AND target_kind='entries' AND target_id=$2", f.identity.HouseholdID, entry).Scan(&after); err != nil || before != after {
		t.Fatal("pruned retry executed another writer", before, after, err)
	}
}

func TestFinanceAuthorityHiddenRelatedRefundDeniesBeforeCapacity(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	other := financeAuthorityMember(t, f, "editor")
	category, original := financeNativeCategory(t, f), value.UUID()
	posted := financeNativeApply(t, f, "entries", "post", original, 0, financeNativeEntry(category, "100"))
	refundInput := financeNativeEntry(category, "-20")
	refundInput.Kind = "refund"
	raw, err := json.Marshal(Record{"original_entry_id": original, "original_version": posted["saved_version"], "entry": refundInput})
	if err != nil {
		t.Fatal(err)
	}
	generation, stamp := financeNativeScope(t, f, "entries", "", 0)
	responsible := f.identity.MembershipID
	command := FinanceCommand{Resource: "entries", Action: "refund", ID: value.UUID(), Generation: generation, AccessVersion: stamp, Payload: raw,
		InitialAccess: &FinanceInitialAccess{AccessMode: "selected", ResponsibleMembershipID: &responsible,
			Grants: []FinanceInitialGrant{{MembershipID: responsible, Permission: "edit"}}}}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal("real private related refund preview", err)
	}
	command.ReviewHash = String(preview["impact_hash"])
	if _, err = f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); err != nil {
		t.Fatal("real private related refund apply", err)
	}
	if _, err = f.store.FinanceView(f.ctx, other, FinanceQuery{Pane: "entries", ID: original}); err != nil {
		t.Fatal("independently shared original became hidden", err)
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := readFinancialScope(f.ctx, tx, other)
	if err != nil {
		rollback(tx)
		t.Fatal(err)
	}
	stamp, err = financeProspectiveAccess(f.ctx, tx, other, scope, nil, "view")
	rollback(tx)
	if err != nil {
		t.Fatal(err)
	}
	refundInput = financeNativeEntry(category, "-10")
	refundInput.Kind = "refund"
	raw, err = json.Marshal(Record{"original_entry_id": original, "original_version": posted["saved_version"], "entry": refundInput})
	if err != nil {
		t.Fatal(err)
	}
	command = FinanceCommand{Resource: "entries", Action: "refund", ID: value.UUID(), Generation: generation, AccessVersion: stamp, Payload: raw}
	if _, err = f.store.FinancePreview(f.ctx, other, value.UUID(), value.UUID(), command); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatal("hidden related refund leaked remaining capacity or permitted another derived write", err)
	}
}
