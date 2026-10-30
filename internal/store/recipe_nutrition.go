package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type RecipeProfileInput struct {
	Generation    int64             `json:"generation"`
	Version       int64             `json:"version"`
	Fingerprint   string            `json:"fingerprint"`
	Basis         string            `json:"basis"`
	BasisServings string            `json:"basis_servings"`
	Source        string            `json:"source"`
	SourceDate    string            `json:"source_date"`
	Nutrients     map[string]string `json:"nutrients"`
	Allergens     map[string]string `json:"allergens"`
}

var recipeNutrientCodes = map[string]bool{
	"energy_kcal": true, "protein_g": true, "fat_g": true,
	"saturated_fat_g": true, "carbohydrate_g": true, "sugars_g": true,
	"fiber_g": true, "salt_g": true, "sodium_mg": true,
}

var recipeAllergenCodes = map[string]bool{
	"gluten": true, "crustaceans": true, "eggs": true, "fish": true,
	"peanuts": true, "soy": true, "milk": true, "tree_nuts": true,
	"celery": true, "mustard": true, "sesame": true, "sulphites": true,
	"lupin": true, "molluscs": true,
}

var recipeNutrientDecimal = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,6})?$`)
var recipePositivePattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,6})?$`)
var recipeRatioPattern = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,6})?$`)

func recipeSource(raw string) (string, bool) {
	if raw != strings.TrimSpace(raw) || utf8.RuneCountInString(raw) < 1 ||
		utf8.RuneCountInString(raw) > 256 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", false
	}
	return raw, true
}

func validateRecipeProfile(in RecipeProfileInput) error {
	if in.Basis == "" {
		if in.BasisServings != "" || in.Source != "" || in.SourceDate != "" ||
			len(in.Nutrients) != 0 || len(in.Allergens) != 0 {
			return Invalid{"Profil vollständig leeren oder Quelle und Werte angeben"}
		}
		return nil
	}
	if in.Basis != "per_recipe" && in.Basis != "per_serving" {
		return Invalid{"Nährwertbasis muss Rezept oder Portion sein"}
	}
	if n, err := value.Decimal(in.BasisServings); !recipePositivePattern.MatchString(in.BasisServings) || err != nil || n.Sign() <= 0 {
		return Invalid{"Nährwertbasis benötigt positive Originalportionen"}
	}
	if _, ok := recipeSource(in.Source); !ok || !value.Date(in.SourceDate) {
		return Invalid{"Nährwertquelle und gültiges Quelldatum erforderlich"}
	}
	if len(in.Nutrients) > 9 || len(in.Allergens) > 14 {
		return Invalid{"Zu viele Nährwert- oder Allergenfelder"}
	}
	for code, amount := range in.Nutrients {
		if !recipeNutrientCodes[code] || !recipeNutrientDecimal.MatchString(amount) {
			return Invalid{"Nährwerte müssen bekannte, nichtnegative Dezimalwerte sein"}
		}
	}
	for code, status := range in.Allergens {
		if !recipeAllergenCodes[code] || status != "contains" && status != "may_contain" && status != "reviewed_absent" {
			return Invalid{"Allergenstatus muss ausdrücklich geprüft sein"}
		}
	}
	return nil
}

func (s *Store) SaveRecipeProfile(ctx context.Context, id auth.Identity, rid, key, requestID string,
	in RecipeProfileInput) (Record, error) {
	if !value.ValidUUID(rid) || in.Version <= 0 || in.Fingerprint == "" {
		return nil, Invalid{"Aktuellen Rezeptstand und Fingerabdruck angeben"}
	}
	if err := validateRecipeProfile(in); err != nil {
		return nil, err
	}
	return s.recipeExpansionWrite(ctx, id, in.Generation, key, requestID,
		"recipes/profile/"+rid, "recipes", rid, in,
		func(ctx context.Context, tx pgx.Tx) (Record, error) {
			bundle, err := recipeLockedBundle(ctx, tx, id, rid)
			if err != nil {
				return nil, err
			}
			if bundle.Fingerprint != in.Fingerprint || Int(bundle.Recipe["version"]) != in.Version {
				return nil, ErrConflict
			}
			profile := Record{}
			if in.Basis != "" {
				var ingredientFingerprint string
				err = tx.QueryRow(ctx, `SELECT recipes.ingredient_profile_fingerprint($1,$2)`,
					id.HouseholdID, rid).Scan(&ingredientFingerprint)
				if err != nil {
					return nil, err
				}
				profile = Record{
					"basis": in.Basis, "basis_servings": in.BasisServings,
					"source": in.Source, "source_date": in.SourceDate,
					"ingredient_fingerprint": ingredientFingerprint,
					"nutrients":              in.Nutrients, "allergens": in.Allergens,
				}
			}
			encoded, err := json.Marshal(profile)
			if err != nil {
				return nil, err
			}
			var raw []byte
			err = tx.QueryRow(ctx, `UPDATE recipes.recipes
 SET nutrition_profile=$3::jsonb,updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2
 RETURNING jsonb_build_object('id',id,'version',version,'nutrition_profile',nutrition_profile)`,
				id.HouseholdID, rid, encoded).Scan(&raw)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			return decode(raw)
		})
}
