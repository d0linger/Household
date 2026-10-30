package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strconv"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// CurrentRecipeFingerprint is the exact PostgreSQL JSONB snapshot digest used
// by recipes.versions. It is a local update fence, not a portable import digest.
func (s *Store) CurrentRecipeFingerprint(ctx context.Context, id auth.Identity, rid string) (string, error) {
	if !value.ValidUUID(rid) {
		return "", ErrNotFound
	}
	var digest string
	err := s.Pool.QueryRow(ctx, `SELECT encode(sha256(convert_to(recipes.content_snapshot($1,$2)::text,'UTF8')),'hex')
	 WHERE EXISTS(SELECT 1 FROM recipes.recipes WHERE household_id=$1 AND id=$2 AND archived_at IS NULL)`, id.HouseholdID, rid).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return digest, nil
}

func recipeLockedBundle(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string) (RecipeBundle, error) {
	if !value.ValidUUID(rid) {
		return RecipeBundle{}, ErrNotFound
	}
	var locked string
	err := tx.QueryRow(ctx, `SELECT id FROM recipes.recipes WHERE household_id=$1 AND id=$2 AND archived_at IS NULL FOR UPDATE NOWAIT`,
		id.HouseholdID, rid).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecipeBundle{}, ErrNotFound
	}
	if err != nil {
		return RecipeBundle{}, shoppingContention(err)
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT recipes.content_snapshot($1,$2)`, id.HouseholdID, rid).Scan(&raw); err != nil {
		return RecipeBundle{}, err
	}
	return recipeBundle(raw)
}

// recipeExpansionWrite gives every new recipe metadata command the same session,
// generation, idempotency and audit fence as the existing workspace commands.
func (s *Store) recipeExpansionWrite(ctx context.Context, id auth.Identity, generation int64,
	key, requestID, operation, entity, rid string, input any,
	write func(context.Context, pgx.Tx) (Record, error)) (Record, error) {
	if !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Vorgangs-ID erforderlich"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "recipes", generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id.UserID+operation+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, id.HouseholdID, id.UserID, operation, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	result, err := write(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "recipes", entity, rid, "record_write", requestID); err != nil {
		return nil, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records
 (household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'recipes',$6,200,$7)`,
		id.HouseholdID, id.UserID, operation, key, hash[:], generation, response)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

// RecipeInsights reads recipe, catalogue and optional Shopping prices from one
// repeatable snapshot. No disabled or previous-generation price is presented.
func (s *Store) RecipeInsights(ctx context.Context, id auth.Identity, rid, servings string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	device, err := recipeScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var recipeState, shoppingState string
	var recipeGeneration, shoppingGeneration int64
	err = tx.QueryRow(ctx, `SELECT r.state,r.data_generation,s.state,s.data_generation
 FROM core.module_states r JOIN core.module_states s ON s.household_id=r.household_id
 WHERE r.household_id=$1 AND r.module_id='recipes' AND s.module_id='shopping'`,
		id.HouseholdID).Scan(&recipeState, &recipeGeneration, &shoppingState, &shoppingGeneration)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && recipeState != "enabled" {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if device.Generation != recipeGeneration {
		return nil, ErrConflict
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT recipes.content_snapshot(r.household_id,r.id)
	 FROM recipes.recipes r WHERE r.household_id=$1 AND r.id=$2 AND r.archived_at IS NULL`,
		id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	bundle, err := recipeBundle(raw)
	if err != nil {
		return nil, err
	}
	base, ok := recipePositiveDecimal(String(bundle.Recipe["base_servings"]))
	if !ok {
		return nil, Invalid{"Rezeptportionen sind ungültig"}
	}
	if servings == "" {
		servings = String(bundle.Recipe["base_servings"])
	}
	selected, ok := recipePositiveDecimal(servings)
	if !ok {
		return nil, Invalid{"Portionen: positive Menge mit höchstens sechs Nachkommastellen erforderlich"}
	}
	ids, seen := []string{}, map[string]bool{}
	for _, ingredient := range bundle.Ingredients {
		catalog := String(ingredient["catalog_id"])
		if value.ValidUUID(catalog) && !seen[catalog] {
			seen[catalog] = true
			ids = append(ids, catalog)
		}
	}
	catalogs := map[string]Record{}
	if len(ids) != 0 {
		rows, queryErr := tx.Query(ctx, `SELECT jsonb_build_object(
 'catalog_id',c.id,'catalog_name',c.name,'catalog_version',c.version,
 'rules',coalesce((SELECT jsonb_agg(rule.value||jsonb_build_object(
   'target_name',t.name,'target_version',t.version,'version',c.version,
   'status',CASE WHEN t.id IS NULL THEN 'unavailable' ELSE 'current' END)
   ORDER BY rule.ordinality)
   FROM jsonb_array_elements(c.substitution_rules) WITH ORDINALITY rule(value,ordinality)
   LEFT JOIN recipes.ingredient_catalog t ON t.household_id=c.household_id
    AND t.id=(rule.value->>'target_catalog_id')::uuid AND NOT t.archived),'[]'::jsonb),
 'density_g_per_ml',c.density_g_per_ml::text,'density_source',c.density_source,
 'density_observed_on',c.density_observed_on,
 'binding_id',b.id,'binding_version',b.version,
 'shopping_generation',b.shopping_generation,
 'product_id',p.id,'product_version',p.version,'product_name',p.name,
 'price_minor',price.price_minor::text,'price_quantity',price.quantity::text,
 'price_unit_code',price.unit_code,'price_observed_on',price.observed_on,
 'price_store',price.store_name)
 FROM recipes.ingredient_catalog c
 LEFT JOIN core.ingredient_product_bindings b ON b.household_id=c.household_id
  AND b.catalog_id=c.id AND b.source_generation=$3
 LEFT JOIN shopping.products p ON p.household_id=c.household_id AND p.id=b.shopping_product_id
  AND $4='enabled' AND b.shopping_generation=$5
 LEFT JOIN LATERAL (SELECT o.price_minor,o.quantity,o.unit_code,o.observed_on,
  st.name AS store_name FROM shopping.price_observations o
  JOIN shopping.stores st ON st.household_id=o.household_id AND st.id=o.store_id
  WHERE o.household_id=c.household_id AND o.product_id=p.id AND o.observed_on<=CURRENT_DATE
  ORDER BY o.observed_on DESC,o.created_at DESC,o.id DESC LIMIT 1) price ON p.id IS NOT NULL
 WHERE c.household_id=$1 AND c.id=ANY($2::uuid[]) AND NOT c.archived LIMIT 501`,
			id.HouseholdID, ids, recipeGeneration, shoppingState, shoppingGeneration)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			var item []byte
			if err = rows.Scan(&item); err != nil {
				rows.Close()
				return nil, err
			}
			catalog, decodeErr := decode(item)
			if decodeErr != nil {
				rows.Close()
				return nil, decodeErr
			}
			catalogs[String(catalog["catalog_id"])] = catalog
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	var ingredientFingerprint string
	if err = tx.QueryRow(ctx, `SELECT recipes.ingredient_profile_fingerprint($1,$2)`,
		id.HouseholdID, rid).Scan(&ingredientFingerprint); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return recipeBuildInsights(bundle, catalogs, selected, base, servings, recipeGeneration,
		shoppingGeneration, shoppingState == "enabled", ingredientFingerprint, device), nil
}

func recipeBuildInsights(bundle RecipeBundle, catalogs map[string]Record,
	selected, base *big.Rat, servings string, recipeGeneration, shoppingGeneration int64,
	shoppingEnabled bool, ingredientFingerprint string, device RecipeDeviceContext) Record {
	substitutions := []Record{}
	availableUnits := map[string]map[string]bool{}
	for _, ingredient := range bundle.Ingredients {
		catalogID, unit := String(ingredient["catalog_id"]), String(ingredient["unit_code"])
		_, quantityOK := recipePositiveDecimal(String(ingredient["quantity"]))
		if !value.ValidUUID(catalogID) || !recipeUnitAllowed(unit) || !quantityOK {
			continue
		}
		if availableUnits[catalogID] == nil {
			availableUnits[catalogID] = map[string]bool{}
		}
		availableUnits[catalogID][unit] = true
	}
	ordered := make([]string, 0, len(catalogs))
	for catalogID := range catalogs {
		ordered = append(ordered, catalogID)
	}
	sort.Strings(ordered)
	for _, catalogID := range ordered {
		catalog := catalogs[catalogID]
		rules, _ := catalog["rules"].([]any)
		for _, candidate := range rules {
			rule, ok := candidate.(map[string]any)
			if ok && String(rule["status"]) == "current" &&
				!availableUnits[catalogID][String(rule["source_unit_code"])] {
				rule["status"] = "inapplicable_unit"
			}
		}
		substitutions = append(substitutions, Record{
			"catalog_id": catalog["catalog_id"], "catalog_name": catalog["catalog_name"],
			"catalog_version": catalog["catalog_version"], "rules": rules,
		})
	}
	lines, conversions := []Record{}, []Record{}
	known := new(big.Rat)
	unknown := 0
	for _, ingredient := range bundle.Ingredients {
		catalogID := String(ingredient["catalog_id"])
		catalog := catalogs[catalogID]
		line, _, amount := recipeIngredientInsight(ingredient, catalog, selected, base, shoppingGeneration)
		lines = append(lines, line)
		conversions = append(conversions, recipeConversionOptions(ingredient, catalog, selected, base)...)
		if amount == nil {
			unknown++
		} else {
			known.Add(known, amount)
		}
	}
	profile, _ := bundle.Recipe["nutrition_profile"].(map[string]any)
	if profile == nil {
		profile = Record{}
	}
	stale := len(profile) > 0 && String(profile["ingredient_fingerprint"]) != ingredientFingerprint
	scaled := Record{}
	statuses := Record{}
	for code := range recipeAllergenCodes {
		statuses[code] = "unknown"
	}
	if !stale && len(profile) > 0 {
		basisServings, ok := recipePositiveDecimal(String(profile["basis_servings"]))
		if ok {
			factor := new(big.Rat).Set(selected)
			if String(profile["basis"]) == "per_recipe" {
				factor.Quo(factor, basisServings)
			}
			if nutrients, ok := profile["nutrients"].(map[string]any); ok {
				for code, raw := range nutrients {
					nutrient, parseErr := value.Decimal(String(raw))
					if parseErr == nil && nutrient.Sign() >= 0 {
						scaled[code] = recipeDisplayQuantity(new(big.Rat).Mul(nutrient, factor), "")
					}
				}
			}
		}
		if allergens, ok := profile["allergens"].(map[string]any); ok {
			for code, status := range allergens {
				if recipeAllergenCodes[code] {
					statuses[code] = status
				}
			}
		}
	}
	knownMinor, approximate := recipeMoneyMinor(known)
	return Record{
		"recipe": Record{"id": bundle.Recipe["id"], "title": bundle.Recipe["title"],
			"version": bundle.Recipe["version"], "content_fingerprint": bundle.Fingerprint,
			"base_servings": bundle.Recipe["base_servings"], "selected_servings": servings},
		"substitutions": substitutions,
		"cost": Record{"known_minor": knownMinor, "approximate": approximate,
			"unknown_count": unknown, "lines": lines},
		"nutrition":   Record{"profile": profile, "stale": stale, "scaled_nutrients": scaled},
		"allergens":   Record{"status_by_code": statuses, "stale": stale},
		"conversions": conversions,
		"context": Record{"recipes_generation": recipeGeneration,
			"shopping_generation": shoppingGeneration, "shopping_enabled": shoppingEnabled,
			"session_scope": device.SessionScope, "auth_scope": device.AuthScope},
	}
}

func recipeIngredientInsight(ingredient, catalog Record, selected, base *big.Rat,
	shoppingGeneration int64) (Record, Record, *big.Rat) {
	line := Record{"ingredient_id": ingredient["id"], "ingredient_name": ingredient["name"],
		"catalog_id": ingredient["catalog_id"], "catalog_version": catalog["catalog_version"],
		"binding_id": catalog["binding_id"], "binding_version": catalog["binding_version"],
		"product_id": catalog["product_id"], "product_version": catalog["product_version"],
		"shopping_generation": shoppingGeneration, "store": catalog["price_store"],
		"observed_on": catalog["price_observed_on"], "unit_code": ingredient["unit_code"],
		"quantity": ingredient["quantity"]}
	conversion := Record{"ingredient_id": ingredient["id"], "catalog_id": ingredient["catalog_id"],
		"catalog_version": catalog["catalog_version"], "source_unit": ingredient["unit_code"],
		"density_g_per_ml": catalog["density_g_per_ml"],
		"source":           catalog["density_source"], "source_date": catalog["density_observed_on"],
		"provenance": ingredient["conversion_provenance"], "status": "unavailable"}
	var density *big.Rat
	if raw := String(catalog["density_g_per_ml"]); raw != "" {
		density, _ = value.Decimal(raw)
		conversion["status"] = "available"
	}
	quantity, ok := recipePositiveDecimal(String(ingredient["quantity"]))
	if !ok || String(ingredient["unit_code"]) == "" {
		line["status"] = "unknown_quantity"
		return line, conversion, nil
	}
	quantity.Mul(quantity, selected).Quo(quantity, base)
	if catalog == nil {
		line["status"] = "unmapped_catalog"
		return line, conversion, nil
	}
	if catalog["product_id"] == nil {
		line["status"] = "unmapped_product"
		return line, conversion, nil
	}
	priceMinor, priceErr := strconv.ParseInt(String(catalog["price_minor"]), 10, 64)
	priceQuantity, quantityOK := recipePositiveDecimal(String(catalog["price_quantity"]))
	if priceErr != nil || priceMinor <= 0 || priceMinor > 1000000000000 || !quantityOK {
		line["status"] = "missing_price"
		return line, conversion, nil
	}
	converted, conversionOK := recipeConvertQuantity(quantity, String(ingredient["unit_code"]),
		String(catalog["price_unit_code"]), density)
	if !conversionOK {
		line["status"] = "incompatible_unit"
		return line, conversion, nil
	}
	amount := new(big.Rat).Mul(new(big.Rat).SetInt64(priceMinor), converted)
	amount.Quo(amount, priceQuantity)
	if amount.Cmp(big.NewRat(1000000000000, 1)) > 0 {
		line["status"] = "amount_too_large"
		return line, conversion, nil
	}
	minor, approximate := recipeMoneyMinor(amount)
	line["status"], line["amount_minor"], line["approximate"] = "priced", minor, approximate
	line["price_unit_code"], line["price_quantity"] = catalog["price_unit_code"], catalog["price_quantity"]
	conversion["target_unit"] = catalog["price_unit_code"]
	conversion["converted"] = recipeDisplayQuantity(converted, String(catalog["price_unit_code"]))
	return line, conversion, amount
}

func recipeConversionOptions(ingredient, catalog Record, selected, base *big.Rat) []Record {
	options := make([]Record, 0, 7)
	from := String(ingredient["unit_code"])
	quantity, valid := recipePositiveDecimal(String(ingredient["quantity"]))
	if valid {
		quantity.Mul(quantity, selected).Quo(quantity, base)
	}
	var density *big.Rat
	if raw := String(catalog["density_g_per_ml"]); raw != "" {
		density, _ = value.Decimal(raw)
	}
	for _, target := range []string{"piece", "g", "kg", "ml", "l", "tsp", "tbsp"} {
		option := Record{
			"ingredient_id": ingredient["id"], "ingredient_name": ingredient["name"],
			"catalog_id": ingredient["catalog_id"], "catalog_version": catalog["catalog_version"],
			"source_unit": from, "target_unit": target,
			"density_g_per_ml": catalog["density_g_per_ml"],
			"source":           catalog["density_source"], "source_date": catalog["density_observed_on"],
			"provenance": ingredient["conversion_provenance"], "status": "unavailable",
		}
		if valid {
			if converted, ok := recipeConvertQuantity(quantity, from, target, density); ok {
				display := recipeDisplayQuantity(converted, target)
				option["status"] = "exact"
				option["quantity"] = display.Display
				option["exact"] = display.Exact
				option["approximate"] = display.Approximate
			}
		}
		options = append(options, option)
	}
	return options
}
