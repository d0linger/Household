package web

import (
	"net/url"

	"household/internal/module"
	"household/internal/store"
	"household/internal/value"
)

// Record destinations open the working object rather than its storage form.
// Generic metadata URLs remain available from the contextual details links.
func workspaceRecordURL(mid string, entity module.Entity, record store.Record) string {
	base := "/m/" + mid + "/" + entity.ID
	id := store.String(record["id"])
	if !value.ValidUUID(id) {
		return base
	}
	switch {
	case mid == "expenses" && entity.ID == "entries":
		return "/m/expenses/editor?entry=" + id
	case mid == "recipes" && entity.ID == "recipes":
		return base + "/" + id + "/workspace"
	case mid == "mealplan" && entity.ID == "plans":
		return "/m/mealplan/workspace/" + id
	case mid == "mealplan" && entity.ID == "meals" && value.ValidUUID(store.String(record["plan_id"])):
		query := url.Values{"meal": {id}}
		if day := store.String(record["planned_on"]); day != "" {
			query.Set("week", day)
		}
		return "/m/mealplan/workspace/" + store.String(record["plan_id"]) + "?" + query.Encode()
	default:
		return base + "/" + id
	}
}
