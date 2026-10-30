package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// RecipeDiscoverFilter is the normalized, bounded filter accepted by the
// recipe discovery API. Cursor coordinates are supplied only after the HTTP
// layer verifies their HMAC, owner, filter and generation scope.
type RecipeDiscoverFilter struct {
	Query                  string
	Tags                   []string
	Catalog                []string
	MaxMinutes             int
	IncludeUnknownDuration bool
	CookedAfter            *time.Time
	NeverCooked            bool
	BeforeUpdatedAt        *time.Time
	BeforeID               string
}

type RecipeDiscoverItem struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	PrepMinutes      int       `json:"prep_minutes"`
	CookMinutes      int       `json:"cook_minutes"`
	LastCookedOn     *string   `json:"last_cooked_on"`
	IngredientLabels []string  `json:"ingredient_labels"`
	TagLabels        []string  `json:"tag_labels"`
	UpdatedAt        time.Time `json:"-"`
}

type RecipeDiscoverPage struct {
	Items   []RecipeDiscoverItem `json:"items"`
	Context RecipeDeviceContext  `json:"context"`
	Next    string               `json:"next"`
	HasMore bool                 `json:"-"`
}

func validRecipeDiscoverFilter(f RecipeDiscoverFilter) error {
	if !utf8.ValidString(f.Query) || utf8.RuneCountInString(f.Query) > 100 ||
		strings.IndexFunc(f.Query, unicode.IsControl) >= 0 ||
		len(f.Tags) > 5 || len(f.Catalog) > 5 ||
		f.MaxMinutes < 0 || f.MaxMinutes > 10080 || f.NeverCooked && f.CookedAfter != nil {
		return Invalid{Message: "Rezeptfilter ist ungültig oder zu umfangreich"}
	}
	if f.BeforeUpdatedAt != nil && !value.ValidUUID(f.BeforeID) {
		return Invalid{Message: "Rezeptseite ist ungültig"}
	}
	for _, ids := range [][]string{f.Tags, f.Catalog} {
		seen := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if !value.ValidUUID(id) {
				return Invalid{Message: "Filterreferenz ist ungültig"}
			}
			if _, exists := seen[id]; exists {
				return Invalid{Message: "Filterreferenz ist doppelt"}
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

// DiscoverRecipes performs filtering in PostgreSQL before the 51-row page
// bound. No preselected catalogue or history slice may silently lose matches.
func (s *Store) DiscoverRecipes(ctx context.Context, id auth.Identity, f RecipeDiscoverFilter) (RecipeDiscoverPage, error) {
	page := RecipeDiscoverPage{Items: []RecipeDiscoverItem{}}
	f.Query = strings.TrimSpace(f.Query)
	if err := validRecipeDiscoverFilter(f); err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer recipeRollback(tx)
	page.Context, err = recipeScope(ctx, tx, id)
	if err != nil {
		return page, err
	}
	var validTags, validCatalog int
	err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM recipes.tags WHERE household_id=$1 AND id=ANY($2::uuid[])),
 (SELECT count(*) FROM recipes.ingredient_catalog WHERE household_id=$1 AND id=ANY($3::uuid[]) AND NOT archived)`, id.HouseholdID, f.Tags, f.Catalog).Scan(&validTags, &validCatalog)
	if err != nil {
		return page, err
	}
	if validTags != len(f.Tags) || validCatalog != len(f.Catalog) {
		return page, Invalid{Message: "Eine gespeicherte Filterreferenz fehlt oder ist archiviert. Auswahl prüfen."}
	}
	var cookedAfter any
	if f.CookedAfter != nil {
		cookedAfter = f.CookedAfter.UTC().Format("2006-01-02")
	}
	var beforeUpdated any
	var beforeID any
	if f.BeforeUpdatedAt != nil {
		beforeUpdated, beforeID = f.BeforeUpdatedAt.UTC(), f.BeforeID
	}
	rows, err := tx.Query(ctx, `SELECT r.id,r.title,r.summary,r.prep_minutes,r.cook_minutes,
 (SELECT max(cr.cooked_on)::text FROM recipes.versions v JOIN recipes.cooking_records cr
  ON cr.household_id=v.household_id AND cr.source_version_id=v.id
  WHERE v.household_id=r.household_id AND v.recipe_id=r.id AND cr.cooked_on<=CURRENT_DATE),
 ARRAY(SELECT i.name FROM recipes.ingredients i WHERE i.household_id=r.household_id AND i.recipe_id=r.id ORDER BY i.position,i.id LIMIT 6),
 ARRAY(SELECT t.name FROM recipes.recipe_tags rt JOIN recipes.tags t ON t.household_id=rt.household_id AND t.id=rt.tag_id
  WHERE rt.household_id=r.household_id AND rt.recipe_id=r.id ORDER BY t.name,t.id LIMIT 6),
 r.updated_at
 FROM recipes.recipes r
 WHERE r.household_id=$1 AND r.archived_at IS NULL
 AND ($2::text='' OR strpos(lower(r.title),lower($2::text))>0)
 AND ($3::int=0 OR (r.prep_minutes>0 AND r.cook_minutes>0 AND r.prep_minutes+r.cook_minutes<=$3)
	  OR ($4::boolean AND (r.prep_minutes=0 OR r.cook_minutes=0)))
 AND NOT EXISTS(SELECT 1 FROM unnest($5::uuid[]) wanted(id) WHERE NOT EXISTS(
  SELECT 1 FROM recipes.recipe_tags rt WHERE rt.household_id=r.household_id AND rt.recipe_id=r.id AND rt.tag_id=wanted.id))
 AND NOT EXISTS(SELECT 1 FROM unnest($6::uuid[]) wanted(id) WHERE NOT EXISTS(
  SELECT 1 FROM recipes.ingredients i WHERE i.household_id=r.household_id AND i.recipe_id=r.id AND i.catalog_id=wanted.id))
 AND ($7::date IS NULL OR EXISTS(SELECT 1 FROM recipes.versions v JOIN recipes.cooking_records cr
  ON cr.household_id=v.household_id AND cr.source_version_id=v.id
  WHERE v.household_id=r.household_id AND v.recipe_id=r.id AND cr.cooked_on BETWEEN $7::date AND CURRENT_DATE))
 AND (NOT $8::boolean OR NOT EXISTS(SELECT 1 FROM recipes.versions v JOIN recipes.cooking_records cr
  ON cr.household_id=v.household_id AND cr.source_version_id=v.id
  WHERE v.household_id=r.household_id AND v.recipe_id=r.id AND cr.cooked_on<=CURRENT_DATE))
 AND ($9::timestamptz IS NULL OR (r.updated_at,r.id)<($9::timestamptz,$10::uuid))
 ORDER BY r.updated_at DESC,r.id DESC LIMIT 51`,
		id.HouseholdID, f.Query, f.MaxMinutes, f.IncludeUnknownDuration, f.Tags, f.Catalog,
		cookedAfter, f.NeverCooked, beforeUpdated, beforeID)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return page, Invalid{Message: "Rezeptsuche dauerte zu lange. Filter eingrenzen und erneut starten."}
		}
		return page, err
	}
	for rows.Next() {
		var item RecipeDiscoverItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Summary, &item.PrepMinutes, &item.CookMinutes,
			&item.LastCookedOn, &item.IngredientLabels, &item.TagLabels, &item.UpdatedAt); err != nil {
			rows.Close()
			return page, err
		}
		if len(page.Items) < 50 {
			page.Items = append(page.Items, item)
		} else {
			page.HasMore = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return page, Invalid{Message: "Rezeptsuche dauerte zu lange. Filter eingrenzen und erneut starten."}
		}
		return page, err
	}
	if err := tx.Commit(ctx); err != nil {
		return page, err
	}
	return page, nil
}
