package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// RecipeConversionInput is a one-row, explicitly chosen operation. The server
// derives original values from the locked recipe version and current catalogue;
// callers cannot supply an arbitrary provenance document.
type RecipeConversionInput struct {
	Kind            string `json:"kind"`
	SourceCatalogID string `json:"source_catalog_id"`
	TargetCatalogID string `json:"target_catalog_id"`
	TargetVersion   int64  `json:"target_version"`
	Ratio           string `json:"ratio,omitempty"`
	SourceUnitCode  string `json:"source_unit_code,omitempty"`
	UnitCode        string `json:"unit_code"`
	RuleVersion     int64  `json:"rule_version,omitempty"`
	DensityGPerML   string `json:"density_g_per_ml,omitempty"`
	Source          string `json:"source"`
	SourceDate      string `json:"source_date"`
}

func exactRecipeDecimal(r *big.Rat) (string, bool) {
	if r == nil || r.Sign() <= 0 || r.Cmp(big.NewRat(1000000, 1)) > 0 {
		return "", false
	}
	printed := r.FloatString(6)
	parsed, ok := new(big.Rat).SetString(printed)
	return printed, ok && parsed.Cmp(r) == 0
}

func saveConvertedRecipeIngredient(ctx context.Context, tx pgx.Tx, id auth.Identity,
	rowID string, version int64, data, previous Record, chosen RecipeConversionInput) (Record, error) {
	if !value.ValidUUID(chosen.SourceCatalogID) || !value.ValidUUID(chosen.TargetCatalogID) ||
		chosen.TargetVersion <= 0 || chosen.TargetVersion > recipeSafeInteger ||
		!recipeUnitAllowed(chosen.UnitCode) || String(data["catalog_id"]) != chosen.TargetCatalogID ||
		String(data["unit_code"]) != chosen.UnitCode ||
		String(previous["catalog_id"]) != chosen.SourceCatalogID {
		return nil, Invalid{Message: "Ersatzzutat und aktueller Katalogstand müssen erneut geprüft werden"}
	}
	oldQuantity, ok := recipePositiveDecimal(String(previous["quantity"]))
	if !ok || !recipeUnitAllowed(String(previous["unit_code"])) {
		return nil, Invalid{Message: "Ausgangszutat benötigt eine bekannte Menge und Einheit"}
	}
	var sourceName, targetName string
	var sourceVersion, targetVersion int64
	var sourceArchived, targetArchived bool
	var ruleRaw []byte
	var density *string
	var densitySource *string
	var densityDate *time.Time
	err := tx.QueryRow(ctx, `SELECT name,version,archived,substitution_rules,density_g_per_ml::text,
	 density_source,density_observed_on FROM recipes.ingredient_catalog
	 WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT`, id.HouseholdID, chosen.SourceCatalogID).
		Scan(&sourceName, &sourceVersion, &sourceArchived, &ruleRaw, &density, &densitySource, &densityDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	err = tx.QueryRow(ctx, `SELECT name,version,archived FROM recipes.ingredient_catalog
	 WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT`, id.HouseholdID, chosen.TargetCatalogID).
		Scan(&targetName, &targetVersion, &targetArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if sourceArchived || targetArchived || targetVersion != chosen.TargetVersion {
		return nil, ErrConflict
	}
	provenance := map[string]any{
		"kind": chosen.Kind, "source_catalog_id": chosen.SourceCatalogID,
		"target_catalog_id": chosen.TargetCatalogID,
		"source_name":       String(previous["name"]),
		"source_quantity":   String(previous["quantity"]),
		"source_unit_code":  String(previous["unit_code"]),
		"recorded_at":       time.Now().UTC().Format(time.RFC3339Nano),
	}
	var targetQuantity *big.Rat
	switch chosen.Kind {
	case "substitution":
		if chosen.SourceCatalogID == chosen.TargetCatalogID || chosen.RuleVersion != sourceVersion ||
			String(data["name"]) != targetName || chosen.DensityGPerML != "" ||
			chosen.SourceUnitCode != String(previous["unit_code"]) {
			return nil, ErrConflict
		}
		var rules []RecipeSubstitutionRule
		if err = json.Unmarshal(ruleRaw, &rules); err != nil {
			return nil, err
		}
		found := false
		for _, rule := range rules {
			if rule.TargetCatalogID != chosen.TargetCatalogID || rule.Ratio != chosen.Ratio ||
				rule.SourceUnitCode != chosen.SourceUnitCode ||
				rule.UnitCode != chosen.UnitCode || rule.Source != chosen.Source ||
				rule.SourceDate != chosen.SourceDate {
				continue
			}
			ratio, valid := recipePositiveDecimal(rule.Ratio)
			if !valid {
				return nil, ErrConflict
			}
			targetQuantity = new(big.Rat).Mul(oldQuantity, ratio)
			provenance["ratio"], provenance["rule_version"] = rule.Ratio, sourceVersion
			provenance["source"], provenance["source_date"] = rule.Source, rule.SourceDate
			found = true
			break
		}
		if !found {
			return nil, ErrConflict
		}
	case "density":
		_, fromDimension, fromOK := recipeUnitFactor(String(previous["unit_code"]))
		_, toDimension, toOK := recipeUnitFactor(chosen.UnitCode)
		if chosen.SourceCatalogID != chosen.TargetCatalogID || chosen.RuleVersion != 0 ||
			chosen.UnitCode == String(previous["unit_code"]) ||
			!fromOK || !toOK ||
			(fromDimension != "mass" || toDimension != "volume") &&
				(fromDimension != "volume" || toDimension != "mass") ||
			String(data["name"]) != String(previous["name"]) || sourceName != targetName ||
			density == nil || densitySource == nil || densityDate == nil ||
			*densitySource != chosen.Source || densityDate.Format("2006-01-02") != chosen.SourceDate {
			return nil, ErrConflict
		}
		densityNumber, valid := recipePositiveDecimal(*density)
		chosenNumber, chosenValid := recipePositiveDecimal(chosen.DensityGPerML)
		if !valid || !chosenValid || densityNumber.Cmp(chosenNumber) != 0 {
			return nil, ErrConflict
		}
		targetQuantity, valid = recipeConvertQuantity(oldQuantity, String(previous["unit_code"]),
			chosen.UnitCode, densityNumber)
		if !valid {
			return nil, Invalid{Message: "Diese Umrechnung ist nicht mit belegter Dichte möglich"}
		}
		provenance["density_g_per_ml"], provenance["source"], provenance["source_date"] =
			*density, *densitySource, densityDate.Format("2006-01-02")
	default:
		return nil, Invalid{Message: "Umrechnungsart wird nicht unterstützt"}
	}
	quantity, exact := exactRecipeDecimal(targetQuantity)
	if !exact || String(data["quantity"]) != quantity {
		return nil, Invalid{Message: "Zielmenge ist nicht exakt oder stimmt nicht mit dem Vorschlag überein"}
	}
	encoded, err := json.Marshal(provenance)
	if err != nil || len(encoded) > 2048 {
		return nil, Invalid{Message: "Umrechnungsherkunft ist zu umfangreich"}
	}
	var raw []byte
	err = tx.QueryRow(ctx, `UPDATE recipes.ingredients AS i SET position=$5,section_name=$6,name=$7,
	 catalog_id=$8::uuid,quantity=$9::numeric,unit_code=$10,quantity_note=$11,
	 preparation_note=$12,conversion_provenance=$13::jsonb,updated_at=clock_timestamp(),version=version+1
	 WHERE i.household_id=$1 AND i.id=$2 AND i.recipe_id=$3 AND i.version=$4
	 RETURNING to_jsonb(i)`, id.HouseholdID, rowID, data["recipe_id"], version,
		data["position"], data["section_name"], data["name"], data["catalog_id"],
		data["quantity"], data["unit_code"], data["quantity_note"],
		data["preparation_note"], encoded).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	return decode(raw)
}
