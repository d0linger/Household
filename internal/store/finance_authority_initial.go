package store

import (
	"slices"
	"strings"

	"household/internal/value"
)

func canonicalFinanceInitialAccess(input *FinanceInitialAccess, creator string) (*FinanceInitialAccess, error) {
	if input == nil {
		return nil, nil
	}
	invalid := Invalid{"Vollständige Anfangsfreigabe mit gültigen Mitgliedern und ausdrücklichen Bearbeitungsrechten prüfen"}
	if input.AccessMode != "household" && input.AccessMode != "selected" || input.Grants == nil || len(input.Grants) > 100 {
		return nil, invalid
	}
	out := *input
	out.Grants = append([]FinanceInitialGrant{}, input.Grants...)
	if input.ResponsibleMembershipID != nil {
		if !value.ValidUUID(*input.ResponsibleMembershipID) {
			return nil, invalid
		}
		responsible := strings.ToLower(*input.ResponsibleMembershipID)
		out.ResponsibleMembershipID = &responsible
	}
	seen := make(map[string]string, len(out.Grants))
	for index := range out.Grants {
		grant := &out.Grants[index]
		grant.MembershipID = strings.ToLower(grant.MembershipID)
		if !value.ValidUUID(grant.MembershipID) || grant.Permission != "view" && grant.Permission != "edit" || seen[grant.MembershipID] != "" {
			return nil, invalid
		}
		seen[grant.MembershipID] = grant.Permission
	}
	if out.AccessMode == "selected" && (out.ResponsibleMembershipID == nil || seen[*out.ResponsibleMembershipID] != "edit" || seen[strings.ToLower(creator)] != "edit") {
		return nil, invalid
	}
	slices.SortFunc(out.Grants, func(a, b FinanceInitialGrant) int { return strings.Compare(a.MembershipID, b.MembershipID) })
	return &out, nil
}
