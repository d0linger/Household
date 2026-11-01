package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCanonicalFinanceRefsTypedIdentities(t *testing.T) {
	const batch = "12345678-1234-4123-8123-123456789abc"
	for _, scenario := range []struct {
		name string
		refs []FinanceResourceRef
	}{
		{"audit_cannot_use_uuid_alias", []FinanceResourceRef{{Kind: "audit", ID: batch, AuditID: 1}}},
		{"audit_requires_positive_bigint", []FinanceResourceRef{{Kind: "audit"}}},
		{"row_requires_positive_number", []FinanceResourceRef{{Kind: "import_row", ID: batch}}},
		{"domain_cannot_hide_row_number", []FinanceResourceRef{{Kind: "entries", ID: batch, RowNumber: 1}}},
		{"domain_cannot_hide_audit_id", []FinanceResourceRef{{Kind: "entries", ID: batch, AuditID: 1}}},
		{"kind_is_closed", []FinanceResourceRef{{Kind: "financial_policies", ID: batch}}},
		{"wrong_uuid_shape", []FinanceResourceRef{{Kind: "accounts", ID: "account:1"}}},
		{"source_bound", make([]FinanceResourceRef, 1025)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := canonicalFinanceRefs(scenario.refs); err == nil {
				t.Fatal("ambiguous or unbounded source identity was accepted")
			}
		})
	}
	refs, err := canonicalFinanceRefs([]FinanceResourceRef{
		{Kind: "import_row", ID: strings.ToUpper(batch), RowNumber: 2},
		{Kind: "import_row", ID: batch, RowNumber: 1},
		{Kind: "import_row", ID: batch, RowNumber: 2},
		{Kind: "audit", AuditID: 9223372036854775807},
	})
	if err != nil || len(refs) != 3 || refs[0].Kind != "audit" || refs[0].AuditID != 9223372036854775807 ||
		refs[1].ID != batch || refs[1].RowNumber != 1 || refs[2].RowNumber != 2 {
		t.Fatal("typed actual keys lost exact identity, stable order or case-normalized deduplication")
	}
}

func TestFinanceAuthorityErrorsStayNeutral(t *testing.T) {
	for _, scenario := range []struct {
		code string
		want error
	}{{"P0002", ErrNotFound}, {"42501", ErrForbidden}, {"40001", ErrConflict}, {"40P01", ErrConflict}, {"55P03", ErrConflict}, {"23505", ErrConflict}} {
		got := financeAuthorityError(&pgconn.PgError{Code: scenario.code, Message: "private-account-name-sentinel"})
		if !errors.Is(got, scenario.want) || strings.Contains(got.Error(), "sentinel") {
			t.Fatalf("SQLSTATE %s exposed a private database diagnostic", scenario.code)
		}
	}
}

func TestFinanceMutationRefsPreserveActualOrigins(t *testing.T) {
	const original = "12345678-1234-4123-8123-123456789abc"
	const account = "22345678-1234-4123-8123-123456789abc"
	refs, err := canonicalFinanceRefs(financeMutationRefs("entries", Record{
		"reverses_entry_id": original, "account_id": account, "paid_by_membership_id": account,
		"created_by": account, "category_id": account,
	}))
	if err != nil || len(refs) != 2 || refs[0].Kind != "accounts" || refs[1].Kind != "entries" || refs[1].ID != original {
		t.Fatal("actual original/account requirements were lost or payer/creator/category invented policy authority")
	}
}

func TestFinanceInitialAccessIsExplicitAndDoesNotMutateInput(t *testing.T) {
	const member = "12345678-1234-4123-8123-123456789abc"
	shared, err := canonicalFinanceInitialAccess(&FinanceInitialAccess{AccessMode: "household", Grants: []FinanceInitialGrant{}}, member)
	if err != nil || shared.ResponsibleMembershipID != nil || len(shared.Grants) != 0 {
		t.Fatal("explicit household mode invented responsibility or personal grants", err)
	}
	responsible := strings.ToUpper(member)
	input := &FinanceInitialAccess{AccessMode: "selected", ResponsibleMembershipID: &responsible,
		Grants: []FinanceInitialGrant{{MembershipID: strings.ToUpper(member), Permission: "edit"}}}
	selected, err := canonicalFinanceInitialAccess(input, member)
	if err != nil || *selected.ResponsibleMembershipID != member || selected.Grants[0].MembershipID != member ||
		*input.ResponsibleMembershipID != strings.ToUpper(member) || input.Grants[0].MembershipID != strings.ToUpper(member) {
		t.Fatal("initial policy validation changed the signed caller input", err)
	}
	input.Grants[0].Permission = "view"
	if _, err = canonicalFinanceInitialAccess(input, member); err == nil {
		t.Fatal("selected create accepted an inferred creator/responsible edit grant")
	}
}
