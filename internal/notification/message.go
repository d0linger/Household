package notification

// ValidMessage validates only the closed wire reference. The caller must decode
// with duplicate/unknown-field rejection and must separately prove authority.
// Schema 1 stays readable for previously queued shopping notifications.
func ValidMessage(schema int, kind, id, scope string, expires, now int64) bool {
	if !canonicalID(id) || len(scope) != 64 || now < 0 || expires <= now || expires > MaxRevision {
		return false
	}
	for _, c := range scope {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	if schema == 1 {
		return kind == "shopping_notice" && expires-now <= 86400
	}
	if schema != 2 {
		return false
	}
	policy, ok := Lookup(kind)
	return ok && expires-now <= int64(policy.Freshness.Seconds())
}
