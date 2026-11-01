//go:build integration

package store

import (
	"encoding/json"
	"testing"

	"household/internal/value"
)

func TestFinanceEntryDetailExposesOnlyCurrentAccessManagement(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	reader := financeAuthorityMember(t, f, "reader")
	id := value.UUID()
	payload, err := json.Marshal(financeNativeEntry(financeNativeCategory(t, f), "100"))
	if err != nil {
		t.Fatal(err)
	}
	generation, stamp := financeNativeScope(t, f, "entries", id, 0)
	responsible := f.identity.MembershipID
	command := FinanceCommand{Resource: "entries", Action: "post", ID: id, Generation: generation, AccessVersion: stamp, Payload: payload,
		InitialAccess: &FinanceInitialAccess{AccessMode: "selected", ResponsibleMembershipID: &responsible, Grants: []FinanceInitialGrant{
			{MembershipID: responsible, Permission: "edit"}, {MembershipID: reader.MembershipID, Permission: "view"},
		}}}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal(err)
	}
	command.ReviewHash = String(preview["impact_hash"])
	if _, err = f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		manager bool
	}{{"responsible", true}, {"reader", false}} {
		t.Run(tc.name, func(t *testing.T) {
			actor := f.identity
			if !tc.manager {
				actor = reader
			}
			view, readErr := f.store.FinanceView(f.ctx, actor, FinanceQuery{Pane: "entries", ID: id})
			if readErr != nil {
				t.Fatal(readErr)
			}
			detail, ok := view["detail"].(Record)
			if !ok {
				t.Fatal("complete entry detail missing")
			}
			access, ok := detail["access"].(Record)
			if !ok || access["can_view"] != true || access["can_manage"] != tc.manager || access["can_edit"] != tc.manager || String(access["access_version"]) == "" {
				t.Fatal("current policy controls missing or overstated", access)
			}
			grants, ok := access["grants"].([]any)
			if !ok || tc.manager && len(grants) != 2 || !tc.manager && len(grants) != 0 {
				t.Fatal("grant metadata escaped current manager scope", access)
			}
		})
	}
}
