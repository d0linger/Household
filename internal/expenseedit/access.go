package expenseedit

import (
	"slices"
	"strings"

	"household/internal/value"
)

type Grant struct {
	MembershipID string
	Permission   string
}

type AccessInput struct {
	Mode   string
	Grants []Grant
}

type Access struct {
	Mode              string
	OwnerMembershipID string
	Grants            []Grant
}

// NormalizeAccess receives the owner from trusted current state, never a client
// assertion. Activity, household membership, management authority and the last
// active editor must additionally be checked and locked in the database.
func NormalizeAccess(input AccessInput, ownerMembershipID string) (Access, error) {
	out := Access{Mode: input.Mode, OwnerMembershipID: strings.ToLower(ownerMembershipID), Grants: []Grant{}}
	if out.Mode == "" {
		out.Mode = "household"
	}
	if out.OwnerMembershipID != "" && !value.ValidUUID(out.OwnerMembershipID) || out.Mode != "household" && out.Mode != "selected" || len(input.Grants) > 100 {
		return Access{}, Problems{"access": "Begrenzten Zugriffsmodus und verantwortliche Mitgliedschaft prüfen"}
	}
	if out.Mode == "household" {
		if len(input.Grants) != 0 {
			return Access{}, Problems{"access.grants": "Haushaltsfreigabe nicht mit einzelnen Freigaben vermischen"}
		}
		return out, nil
	}
	if out.OwnerMembershipID == "" {
		return Access{}, Problems{"access.owner": "Verantwortung vor eingeschränkter Freigabe ausdrücklich festlegen"}
	}
	seen := make(map[string]bool, len(input.Grants)+1)
	for _, raw := range input.Grants {
		grant := Grant{MembershipID: strings.ToLower(raw.MembershipID), Permission: raw.Permission}
		if !value.ValidUUID(grant.MembershipID) || seen[grant.MembershipID] || grant.Permission != "view" && grant.Permission != "edit" ||
			grant.MembershipID == out.OwnerMembershipID && grant.Permission != "edit" {
			return Access{}, Problems{"access.grants": "Eindeutige Lese-/Bearbeitungsfreigaben und Verantwortungszugriff prüfen"}
		}
		seen[grant.MembershipID] = true
		out.Grants = append(out.Grants, grant)
	}
	if !seen[out.OwnerMembershipID] {
		out.Grants = append(out.Grants, Grant{MembershipID: out.OwnerMembershipID, Permission: "edit"})
	}
	if len(out.Grants) > 100 {
		return Access{}, Problems{"access.grants": "Höchstens100Mitgliedschaften einschließlich Verantwortung freigeben"}
	}
	slices.SortFunc(out.Grants, func(a, b Grant) int { return strings.Compare(a.MembershipID, b.MembershipID) })
	return out, nil
}
