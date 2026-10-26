package expenseedit

import (
	"reflect"
	"strings"
	"testing"

	"household/internal/value"
)

func TestAccessPreservesUnownedSharedLegacyAndCanonicalSelectedOwner(t *testing.T) {
	shared, err := NormalizeAccess(AccessInput{}, "")
	if err != nil || shared.Mode != "household" || shared.OwnerMembershipID != "" || shared.Grants == nil || len(shared.Grants) != 0 {
		t.Fatal("legacy shared data gained an invented owner", shared, err)
	}
	input := AccessInput{Mode: "selected", Grants: []Grant{{MembershipID: strings.ToUpper(categoryFixture), Permission: "view"}}}
	before := append([]Grant(nil), input.Grants...)
	got, err := NormalizeAccess(input, strings.ToUpper(memberFixture))
	if err != nil || got.OwnerMembershipID != memberFixture || len(got.Grants) != 2 || got.Grants[0] != (Grant{MembershipID: memberFixture, Permission: "edit"}) || got.Grants[1].MembershipID != categoryFixture || !reflect.DeepEqual(input.Grants, before) {
		t.Fatal("selected policy lost its trusted owner or changed request-owned data", got, err)
	}
}

func TestAccessDoesNotAcceptAmbiguousOrSelfLockingGrants(t *testing.T) {
	for _, input := range []AccessInput{
		{Mode: "private"}, {Mode: "household", Grants: []Grant{{MembershipID: categoryFixture, Permission: "view"}}},
		{Mode: "selected", Grants: []Grant{{MembershipID: memberFixture, Permission: "view"}}},
		{Mode: "selected", Grants: []Grant{{MembershipID: categoryFixture, Permission: "owner"}}},
		{Mode: "selected", Grants: []Grant{{MembershipID: "invalid", Permission: "view"}}},
		{Mode: "selected", Grants: []Grant{{MembershipID: categoryFixture, Permission: "view"}, {MembershipID: strings.ToUpper(categoryFixture), Permission: "edit"}}},
	} {
		if got, err := NormalizeAccess(input, memberFixture); err == nil || !reflect.DeepEqual(got, Access{}) {
			t.Fatal("ambiguous/invalid access policy accepted", input, got, err)
		}
	}
	if _, err := NormalizeAccess(AccessInput{Mode: "selected"}, ""); err == nil {
		t.Fatal("restricted legacy data accepted without explicit responsibility")
	}
}

func TestAccessBoundIncludesOwnerAndStableOrder(t *testing.T) {
	input := AccessInput{Mode: "selected"}
	for range 99 {
		input.Grants = append(input.Grants, Grant{MembershipID: value.UUID(), Permission: "view"})
	}
	got, err := NormalizeAccess(input, memberFixture)
	if err != nil || len(got.Grants) != 100 {
		t.Fatal("99 explicit members plus trusted owner should fit", len(got.Grants), err)
	}
	for i := 1; i < len(got.Grants); i++ {
		if got.Grants[i-1].MembershipID >= got.Grants[i].MembershipID {
			t.Fatal("grant encoding has unstable order")
		}
	}
	input.Grants = append(input.Grants, Grant{MembershipID: value.UUID(), Permission: "view"})
	if _, err := NormalizeAccess(input, memberFixture); err == nil {
		t.Fatal("owner silently pushed grants past100")
	}
}
