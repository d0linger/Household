package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type RecipeSubstitutionRule struct {
	TargetCatalogID string `json:"target_catalog_id"`
	Ratio           string `json:"ratio"`
	SourceUnitCode  string `json:"source_unit_code"`
	UnitCode        string `json:"unit_code"`
	Source          string `json:"source"`
	SourceDate      string `json:"source_date"`
	Note            string `json:"note"`
}

type RecipeSubstitutionInput struct {
	Generation     int64                    `json:"generation"`
	CatalogVersion int64                    `json:"catalog_version"`
	Rules          []RecipeSubstitutionRule `json:"rules"`
}

type RecipeDensityInput struct {
	Generation     int64  `json:"generation"`
	CatalogVersion int64  `json:"catalog_version"`
	GramsPerML     string `json:"grams_per_ml"`
	Source         string `json:"source"`
	SourceDate     string `json:"source_date"`
}

func recipeCatalogLocked(ctx context.Context, tx pgx.Tx, id auth.Identity, catalog string) (Record, error) {
	if !value.ValidUUID(catalog) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT to_jsonb(c) FROM recipes.ingredient_catalog c
 WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT`, id.HouseholdID, catalog).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	return decode(raw)
}

func recipeCatalogCurrent(current Record, version int64) error {
	if Int(current["version"]) != version {
		return ErrConflict
	}
	if current["archived"] == true {
		return Invalid{"Archivierte Katalogzutat zuerst aktivieren"}
	}
	return nil
}

func validateSubstitutionRules(catalog string, rules []RecipeSubstitutionRule) error {
	if len(rules) > 20 {
		return Invalid{"Höchstens 20 Ersatzvorschläge je Katalogzutat"}
	}
	seen := map[string]bool{}
	for _, rule := range rules {
		if !value.ValidUUID(rule.TargetCatalogID) || rule.TargetCatalogID == catalog || seen[rule.TargetCatalogID] {
			return Invalid{"Ersatz muss eine andere, eindeutige Katalogzutat sein"}
		}
		seen[rule.TargetCatalogID] = true
		ratio, err := value.Decimal(rule.Ratio)
		if !recipeRatioPattern.MatchString(rule.Ratio) || err != nil || ratio.Sign() <= 0 || ratio.Cmp(big.NewRat(1000, 1)) > 0 {
			return Invalid{"Ersatzverhältnis muss positiv und höchstens 1000 sein"}
		}
		if !recipeUnitAllowed(rule.SourceUnitCode) || !recipeUnitAllowed(rule.UnitCode) {
			return Invalid{"Ersatz benötigt unterstützte Ausgangs- und Zieleinheiten"}
		}
		if _, ok := recipeSource(rule.Source); !ok || !value.Date(rule.SourceDate) ||
			utf8.RuneCountInString(rule.Note) > 500 || strings.ContainsAny(rule.Note, "\x00") {
			return Invalid{"Ersatz benötigt gültige Quelle, Datum und kurzen Hinweis"}
		}
	}
	return nil
}

func (s *Store) SaveRecipeSubstitutions(ctx context.Context, id auth.Identity, catalog, key, requestID string,
	in RecipeSubstitutionInput) (Record, error) {
	if !value.ValidUUID(catalog) || in.CatalogVersion <= 0 {
		return nil, Invalid{"Aktuellen Katalogstand angeben"}
	}
	if err := validateSubstitutionRules(catalog, in.Rules); err != nil {
		return nil, err
	}
	return s.recipeExpansionWrite(ctx, id, in.Generation, key, requestID,
		"recipes/substitutions/"+catalog, "ingredient_catalog", catalog, in,
		func(ctx context.Context, tx pgx.Tx) (Record, error) {
			current, err := recipeCatalogLocked(ctx, tx, id, catalog)
			if err != nil {
				return nil, err
			}
			if err = recipeCatalogCurrent(current, in.CatalogVersion); err != nil {
				return nil, err
			}
			targets := make([]string, 0, len(in.Rules))
			for _, rule := range in.Rules {
				targets = append(targets, rule.TargetCatalogID)
			}
			sort.Strings(targets)
			for _, target := range targets {
				var archived bool
				err = tx.QueryRow(ctx, `SELECT archived FROM recipes.ingredient_catalog
 WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT`, id.HouseholdID, target).Scan(&archived)
				if errors.Is(err, pgx.ErrNoRows) || archived {
					return nil, Invalid{"Ersatzziel fehlt oder wurde archiviert"}
				}
				if err != nil {
					return nil, shoppingContention(err)
				}
			}
			rules := in.Rules
			if rules == nil {
				rules = []RecipeSubstitutionRule{}
			}
			encoded, err := json.Marshal(rules)
			if err != nil {
				return nil, err
			}
			var raw []byte
			err = tx.QueryRow(ctx, `UPDATE recipes.ingredient_catalog SET substitution_rules=$3::jsonb,
 updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2
 RETURNING jsonb_build_object('id',id,'version',version,'substitution_rules',substitution_rules)`,
				id.HouseholdID, catalog, encoded).Scan(&raw)
			if err != nil {
				return nil, shoppingContention(err)
			}
			return decode(raw)
		})
}

func (s *Store) SaveRecipeDensity(ctx context.Context, id auth.Identity, catalog, key, requestID string,
	in RecipeDensityInput) (Record, error) {
	if !value.ValidUUID(catalog) || in.CatalogVersion <= 0 {
		return nil, Invalid{"Aktuellen Katalogstand angeben"}
	}
	if in.GramsPerML == "" {
		if in.Source != "" || in.SourceDate != "" {
			return nil, Invalid{"Dichte vollständig leeren oder Quelle und Datum angeben"}
		}
	} else {
		density, err := value.Decimal(in.GramsPerML)
		if !recipePositivePattern.MatchString(in.GramsPerML) || err != nil || density.Sign() <= 0 || density.Cmp(big.NewRat(1000, 1)) > 0 {
			return nil, Invalid{"Dichte muss zwischen 0 und 1000 g/ml liegen"}
		}
		if _, ok := recipeSource(in.Source); !ok || !value.Date(in.SourceDate) {
			return nil, Invalid{"Dichte benötigt Quelle und gültiges Quelldatum"}
		}
	}
	return s.recipeExpansionWrite(ctx, id, in.Generation, key, requestID,
		"recipes/density/"+catalog, "ingredient_catalog", catalog, in,
		func(ctx context.Context, tx pgx.Tx) (Record, error) {
			current, err := recipeCatalogLocked(ctx, tx, id, catalog)
			if err != nil {
				return nil, err
			}
			if err = recipeCatalogCurrent(current, in.CatalogVersion); err != nil {
				return nil, err
			}
			var density, source, observed any
			if in.GramsPerML != "" {
				density, source, observed = in.GramsPerML, in.Source, in.SourceDate
			}
			var raw []byte
			err = tx.QueryRow(ctx, `UPDATE recipes.ingredient_catalog SET density_g_per_ml=$3::numeric,
 density_source=$4,density_observed_on=$5::date,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2
 RETURNING jsonb_build_object('id',id,'version',version,'grams_per_ml',density_g_per_ml::text,
 'source',density_source,'source_date',density_observed_on)`,
				id.HouseholdID, catalog, density, source, observed).Scan(&raw)
			if err != nil {
				return nil, shoppingContention(err)
			}
			return decode(raw)
		})
}
