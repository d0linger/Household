package web

import (
	"context"

	"household/internal/auth"
	"household/internal/i18n"
	"household/internal/store"
)

// Only return previews of current recipes and attachments visible to this session.
func (s *Server) recipeCovers(ctx context.Context, actor auth.Identity, ids []string) map[string]string {
	state, enabled := s.Store.State("recipes")
	if !enabled || len(ids) == 0 || len(ids) > 50 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (r.id) r.id,a.id FROM recipes.recipes r
 JOIN core.attachments a ON a.household_id=r.household_id AND a.resource_id=r.id
 JOIN core.attachment_derivatives d ON d.attachment_id=a.id AND d.variant='preview'
 WHERE r.household_id=$1 AND r.id=ANY($2::uuid[]) AND a.module_id='recipes' AND a.resource_type='recipes'
 AND a.module_generation=$3 AND a.state='ready' AND a.media_type IN ('image/jpeg','image/png')
 AND core.finance_artifact_visible($1,$4,$5,$6,$7,'attachment',a.id,'view')
 AND core.finance_artifact_visible($1,$4,$5,$6,$7,'attachment_derivative',d.id,'view')
 ORDER BY r.id,a.created_at DESC,a.id`, actor.HouseholdID, ids, state.Generation, actor.UserID, actor.MembershipID, actor.SessionID, actor.AuthVersion)
	if err != nil {
		return nil
	}
	defer rows.Close()
	covers := make(map[string]string)
	for rows.Next() {
		var recipe, attachment string
		if rows.Scan(&recipe, &attachment) != nil {
			return nil
		}
		covers[recipe] = "/attachments/" + attachment + "/preview"
	}
	if rows.Err() != nil {
		return nil
	}
	return covers
}

func (s *Server) todayShopping(ctx context.Context, actor auth.Identity, locale string) todaySection {
	rows, err := s.Pool.Query(ctx, `WITH current_list AS (
 SELECT l.id,l.name FROM shopping.lists l WHERE l.household_id=$1 AND l.archived_at IS NULL AND NOT l.is_template
 ORDER BY EXISTS (SELECT 1 FROM shopping.items i WHERE i.household_id=$1 AND i.list_id=l.id AND i.checked_at IS NULL) DESC,l.updated_at DESC,l.id LIMIT 1)
 SELECT l.id,l.name,i.id,i.name,coalesce(i.quantity::text,''),coalesce(i.unit_code,'')
 FROM current_list l LEFT JOIN LATERAL (SELECT id,name,quantity,unit_code,sort_order FROM shopping.items
 WHERE household_id=$1 AND list_id=l.id AND checked_at IS NULL ORDER BY sort_order,id LIMIT 5) i ON true
 ORDER BY i.sort_order,i.id`, actor.HouseholdID)
	if err != nil {
		return newTodaySection("error")
	}
	defer rows.Close()
	out := newTodaySection("ready")
	for rows.Next() {
		var list, quantity, unit string
		var item, name *string
		if err := rows.Scan(&list, &out.Title, &item, &name, &quantity, &unit); err != nil {
			return newTodaySection("error")
		}
		out.URL = "/m/shopping/lists/" + list
		if item == nil {
			continue
		}
		if len(out.Items) == 4 {
			out.More = true
			break
		}
		meta := ""
		if quantity != "" {
			meta = localizedNumeric(locale, formatQuantity(quantity))
			if unit != "" {
				meta += " " + i18n.Text(locale, formatChoice(unit))
			}
		}
		out.Items = append(out.Items, todayItem{ID: *item, Title: *name, Meta: meta, URL: out.URL})
	}
	if rows.Err() != nil {
		return newTodaySection("error")
	}
	return out
}

func (s *Server) todayFinances(ctx context.Context, actor auth.Identity, locale, month string) todaySection {
	view, err := s.Store.FinanceView(ctx, actor, store.FinanceQuery{Pane: "overview", Month: month})
	if err != nil {
		return newTodaySection("error")
	}
	totals, ok := view["totals"].(store.Record)
	if !ok {
		return newTodaySection("error")
	}
	out := newTodaySection("ready")
	const url = "/m/expenses/workspace/overview"
	for _, field := range []struct{ key, label string }{{"posted_consumption_minor", "Gebuchte Ausgaben"}, {"draft_known_minor", "Entwürfe mit Betrag"}} {
		minor, ok := totals[field.key].(string)
		if !ok {
			return newTodaySection("error")
		}
		out.Items = append(out.Items, todayItem{Title: i18n.Text(locale, field.label), Minor: minor, URL: url})
	}
	if count, ok := totals["draft_unknown_count"].(int64); ok && count > 0 {
		out.Items = append(out.Items, todayItem{Title: i18n.Text(locale, "Entwürfe ohne Betrag"), Meta: store.String(count), URL: url})
	}
	return out
}
