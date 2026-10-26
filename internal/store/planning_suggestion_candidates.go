package store

import (
	"context"
	"math/big"
	"sort"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const suggestionByteLimit = 16 << 20

func readSuggestionCandidates(ctx context.Context, tx pgx.Tx, id auth.Identity, query RecipeSuggestionQuery, out *RecipeSuggestionPage) error {
	rows, err := tx.Query(ctx, `SELECT r.id,r.created_at,coalesce(v.id::text,''),coalesce(v.revision::text,''),
 coalesce(v.snapshot->'recipe'->>'title',r.title),coalesce(v.snapshot->'recipe'->>'base_servings',''),
 coalesce(octet_length(v.snapshot::text),0),coalesce(jsonb_array_length(v.snapshot->'ingredients'),0)
 FROM recipes.recipes r LEFT JOIN LATERAL (
 SELECT ver.id,ver.revision,ver.snapshot FROM recipes.versions ver WHERE ver.household_id=r.household_id AND ver.recipe_id=r.id
 ORDER BY ver.revision DESC LIMIT 1) v ON true
 WHERE r.household_id=$1 AND r.archived_at IS NULL AND ($2='' OR strpos(lower(r.title),lower($2))>0)
 AND (nullif($3,'')::uuid IS NULL OR (r.created_at,r.id)>($4::timestamptz,nullif($3,'')::uuid))
 ORDER BY r.created_at,r.id LIMIT 21`, id.HouseholdID, out.Search, query.AfterID, query.AfterCreated)
	if err != nil {
		return err
	}
	defer rows.Close()
	var bytes int64
	ingredients := 0
	for rows.Next() {
		candidate := RecipeSuggestion{Outcome: "empty", Lines: []IngredientComparisonLine{}, Products: []suggestionProductCoverage{}, Dates: []suggestionDateReason{}}
		if err = rows.Scan(&candidate.RecipeID, &candidate.created, &candidate.SourceVersionID, &candidate.Revision, &candidate.Title,
			&candidate.BaseServings, &candidate.bytes, &candidate.count); err != nil {
			return err
		}
		if len(out.Candidates) == suggestionPageLimit {
			out.More = true
			continue
		}
		switch {
		case candidate.SourceVersionID == "":
			candidate.SourceIssue = "source_missing"
		case candidate.bytes > 1<<20:
			candidate.SourceIssue = "source_size_limit"
		case candidate.count > planningIngredientLimit:
			candidate.SourceIssue = "source_ingredient_limit"
		default:
			bytes += candidate.bytes
			ingredients += candidate.count
		}
		if bytes > suggestionByteLimit || ingredients > planningIngredientLimit {
			return Invalid{"Bewertete Auswahl auf500 Zutaten und16MiB begrenzen; Suche eingrenzen"}
		}
		out.Candidates = append(out.Candidates, candidate)
		out.AfterCreated, out.AfterID = candidate.created, candidate.RecipeID
	}
	return rows.Err()
}

func readSuggestionIngredients(ctx context.Context, tx pgx.Tx, id auth.Identity, servings *big.Rat, out *RecipeSuggestionPage) (map[string]suggestionProductIdentity, error) {
	products := make(map[string]suggestionProductIdentity)
	versions := make([]string, 0, len(out.Candidates))
	byVersion := make(map[string]int, len(out.Candidates))
	for index, candidate := range out.Candidates {
		if candidate.SourceIssue == "" {
			versions = append(versions, candidate.SourceVersionID)
			byVersion[candidate.SourceVersionID] = index
		}
	}
	if len(versions) == 0 {
		return products, nil
	}
	rows, err := tx.Query(ctx, `SELECT v.id,i.position, jsonb_build_object('id',i.item->>'id','name',i.item->>'name',
 'catalog_id',i.item->>'catalog_id','catalog_label',i.item->>'catalog_label','quantity',i.item->>'quantity','unit_code',i.item->>'unit_code')
 FROM recipes.versions v CROSS JOIN LATERAL jsonb_array_elements(v.snapshot->'ingredients') WITH ORDINALITY i(item,position)
 WHERE v.household_id=$1 AND v.id=ANY($2::uuid[]) ORDER BY v.id,i.position LIMIT 501`, id.HouseholdID, versions)
	if err != nil {
		return nil, err
	}
	type sourceIngredient struct {
		candidate int
		data      Record
	}
	ingredients := make([]sourceIngredient, 0)
	catalogs := make(map[string]bool)
	for rows.Next() {
		var version string
		var position int
		var raw []byte
		if err = rows.Scan(&version, &position, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		index, found := byVersion[version]
		if !found {
			rows.Close()
			return nil, ErrConflict
		}
		ingredient, decodeErr := decode(raw)
		if decodeErr != nil {
			rows.Close()
			return nil, decodeErr
		}
		if catalog := String(ingredient["catalog_id"]); value.ValidUUID(catalog) {
			catalogs[catalog] = true
		}
		ingredients = append(ingredients, sourceIngredient{candidate: index, data: ingredient})
		if len(ingredients) > planningIngredientLimit {
			rows.Close()
			return nil, Invalid{"Mehr als500 Zutaten in der bewerteten Auswahl"}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	catalogIDs := make([]string, 0, len(catalogs))
	for catalog := range catalogs {
		catalogIDs = append(catalogIDs, catalog)
	}
	sort.Strings(catalogIDs)
	mappings, err := comparisonMappings(ctx, tx, id, catalogIDs, out.RecipesGeneration, out.PantryGeneration, out.PantryEnabled)
	if err != nil {
		return nil, err
	}
	for _, ingredient := range ingredients {
		candidate := &out.Candidates[ingredient.candidate]
		base, baseErr := value.Decimal(candidate.BaseServings)
		if baseErr != nil || base.Sign() <= 0 {
			return nil, Invalid{"Rezeptstand enthält keine gültigen Basisportionen"}
		}
		data := ingredient.data
		line := IngredientComparisonLine{ID: String(data["id"]), Name: String(data["name"]), CatalogID: String(data["catalog_id"]), CatalogLabel: String(data["catalog_label"])}
		mapping := mappings[line.CatalogID]
		line.Reason = comparisonMappingReason(line.CatalogID, mapping, out.PantryEnabled)
		quantity := planningDemandQuantity{}
		if line.Reason == "" {
			line.ProductID = String(mapping["product_id"])
			quantity.ProductID, quantity.Unit = line.ProductID, String(mapping["unit"])
			if product, exists := products[line.ProductID]; exists && product.Unit != quantity.Unit {
				return nil, ErrConflict
			}
			products[line.ProductID] = suggestionProductIdentity{Unit: quantity.Unit, Name: String(mapping["product_name"])}
			quantity.Quantity, line.Reason = suggestionIngredientQuantity(data, mapping, base, servings)
			if quantity.Quantity != nil {
				amount, amountErr := ingredientAmount(quantity.Quantity)
				if amountErr != nil {
					return nil, amountErr
				}
				line.Demand = &amount
			}
		}
		candidate.Lines = append(candidate.Lines, line)
		candidate.quantities = append(candidate.quantities, quantity)
	}
	for _, candidate := range out.Candidates {
		if candidate.SourceIssue == "" && len(candidate.Lines) != candidate.count {
			return nil, ErrConflict
		}
	}
	return products, nil
}
