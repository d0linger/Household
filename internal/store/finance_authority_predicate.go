package store

import (
	"fmt"
	"regexp"
)

var financeSQLAlias = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// financeResourcePredicate filters complete current authority in SQL, before
// pagination or aggregation. All kind/alias inputs are compiled server choices.
func financeResourcePredicate(scope financialScope, kind, intent, alias string, first int) (string, []any, error) {
	if !financeKnownKind(kind) || !financeSQLAlias.MatchString(alias) || first < 1 || first > 100 ||
		(intent != "view" && intent != "edit" && intent != "manage" && intent != "preference") ||
		scope.userID == "" || scope.membershipID == "" {
		return "", nil, ErrForbidden
	}
	if intent == "edit" || intent == "manage" {
		if scope.role != "admin" && scope.role != "editor" {
			return "", nil, ErrForbidden
		}
	}
	if intent == "preference" && kind != "saved_filters" {
		return "", nil, ErrForbidden
	}
	// security: only validated compiled identifiers and numeric placeholders are
	// interpolated. Identity, generation, kind and intent remain parameters.
	predicate := fmt.Sprintf(`core.finance_resource_visible($%d,$%d,$%d,nullif($%d,'')::uuid,$%d,$%d,$%d,%s.id,$%d)`,
		first, first+1, first+2, first+3, first+4, first+5, first+6, alias, first+7)
	return predicate, []any{scope.householdID, scope.userID, scope.membershipID, scope.sessionID,
		scope.authVersion, scope.generation, kind, intent}, nil
}
