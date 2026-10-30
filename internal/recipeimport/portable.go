// Package recipeimport turns untrusted recipe files and pages into inert,
// bounded drafts. It never fetches URLs or writes application data.
package recipeimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"household/internal/value"

	"golang.org/x/text/unicode/norm"
)

const (
	MaxInputBytes    = 2 << 20
	MaxExpandedBytes = 4 << 20
	MaxRecipes       = 20
	MaxIngredients   = 500
	MaxSteps         = 500
	MaxTags          = 100
)

type Ingredient struct {
	Name            string `json:"name"`
	Section         string `json:"section,omitempty"`
	Quantity        string `json:"quantity,omitempty"`
	Unit            string `json:"unit,omitempty"`
	QuantityNote    string `json:"quantity_note,omitempty"`
	PreparationNote string `json:"preparation_note,omitempty"`
}

type Step struct {
	Instruction  string `json:"instruction"`
	TimerSeconds int    `json:"timer_seconds,omitempty"`
}

// Recipe contains only portable, intentionally exported recipe content. No
// database identity, actor, session, storage key or audit field belongs here.
type Recipe struct {
	Title        string       `json:"title"`
	Summary      string       `json:"summary,omitempty"`
	Servings     string       `json:"servings"`
	PrepMinutes  int          `json:"prep_minutes,omitempty"`
	CookMinutes  int          `json:"cook_minutes,omitempty"`
	SourceURL    string       `json:"source_url,omitempty"`
	SourceNote   string       `json:"source_note,omitempty"`
	Notes        string       `json:"notes,omitempty"`
	Ingredients  []Ingredient `json:"ingredients"`
	Steps        []Step       `json:"steps"`
	Tags         []string     `json:"tags,omitempty"`
	Losses       []string     `json:"-"`
	SourceFormat string       `json:"-"`
}

type HouseholdFile struct {
	Format  string   `json:"format"`
	Version int      `json:"version"`
	Recipes []Recipe `json:"recipes"`
}

func cleanText(s string, maxBytes int) (string, error) {
	s = norm.NFC.String(strings.TrimSpace(s))
	if len(s) > maxBytes || !utf8.ValidString(s) || strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0 {
		return "", errors.New("unzulässiger oder zu langer Text")
	}
	return s, nil
}

// CanonicalURL stores no query/fragment/userinfo, even if a transient fetch
// URL contained private query parameters. The URL is never fetched here.
func CanonicalURL(raw string) (string, error) {
	return canonicalURL(raw)
}

func Normalize(in Recipe) (Recipe, error) {
	var out Recipe
	var err error
	out.Title, err = cleanText(in.Title, 800)
	if err != nil || out.Title == "" || utf8.RuneCountInString(out.Title) > 200 {
		return out, errors.New("rezeptname fehlt oder ist ungültig")
	}
	out.Summary, err = cleanText(in.Summary, 20000)
	if err != nil {
		return out, fmt.Errorf("beschreibung: %w", err)
	}
	out.SourceNote, err = cleanText(in.SourceNote, 1200)
	if err != nil || utf8.RuneCountInString(out.SourceNote) > 300 {
		return out, fmt.Errorf("quelle: %w", err)
	}
	out.Notes, err = cleanText(in.Notes, 20000)
	if err != nil {
		return out, fmt.Errorf("notizen: %w", err)
	}
	if in.SourceURL != "" {
		out.SourceURL, err = CanonicalURL(in.SourceURL)
		if err != nil {
			return out, fmt.Errorf("Quell-URL: %w", err)
		}
	}
	servings := strings.TrimSpace(in.Servings)
	if servings == "" {
		servings = "1"
	}
	quantity, err := value.Decimal(servings)
	if err != nil || quantity.Sign() <= 0 || quantity.Cmp(valueRatMillion()) > 0 {
		return out, errors.New("portionen müssen eine positive exakte Zahl sein")
	}
	out.Servings = strings.ReplaceAll(servings, ",", ".")
	if in.PrepMinutes < 0 || in.PrepMinutes > 10080 || in.CookMinutes < 0 || in.CookMinutes > 10080 {
		return out, errors.New("zeitangabe liegt außerhalb der Grenze")
	}
	out.PrepMinutes, out.CookMinutes = in.PrepMinutes, in.CookMinutes
	if len(in.Ingredients) > MaxIngredients || len(in.Steps) > MaxSteps || len(in.Tags) > MaxTags {
		return out, errors.New("rezept hat zu viele Zutaten, Schritte oder Tags")
	}
	out.Ingredients = make([]Ingredient, 0, len(in.Ingredients))
	for i, row := range in.Ingredients {
		var item Ingredient
		item.Name, err = cleanText(row.Name, 800)
		if err != nil || item.Name == "" || utf8.RuneCountInString(item.Name) > 200 {
			return out, fmt.Errorf("zutat %d: Name fehlt oder ist ungültig", i+1)
		}
		item.Section, err = cleanText(row.Section, 1200)
		if err != nil || utf8.RuneCountInString(item.Section) > 300 {
			return out, fmt.Errorf("zutat %d: Abschnitt ist ungültig", i+1)
		}
		item.QuantityNote, err = cleanText(row.QuantityNote, 1200)
		if err != nil || utf8.RuneCountInString(item.QuantityNote) > 300 {
			return out, fmt.Errorf("zutat %d: Mengenhinweis ist ungültig", i+1)
		}
		item.PreparationNote, err = cleanText(row.PreparationNote, 1200)
		if err != nil || utf8.RuneCountInString(item.PreparationNote) > 300 {
			return out, fmt.Errorf("zutat %d: Vorbereitung ist ungültig", i+1)
		}
		item.Unit = strings.TrimSpace(row.Unit)
		if item.Unit != "" && item.Unit != "piece" && item.Unit != "g" && item.Unit != "kg" && item.Unit != "ml" && item.Unit != "l" && item.Unit != "tsp" && item.Unit != "tbsp" {
			return out, fmt.Errorf("zutat %d: unbekannte Einheit", i+1)
		}
		if row.Quantity != "" {
			number, parseErr := value.Decimal(row.Quantity)
			if parseErr != nil || number.Sign() <= 0 || number.Cmp(valueRatMillion()) > 0 {
				return out, fmt.Errorf("zutat %d: Menge ist ungültig", i+1)
			}
			item.Quantity = strings.ReplaceAll(strings.TrimSpace(row.Quantity), ",", ".")
		} else if item.Unit != "" {
			return out, fmt.Errorf("zutat %d: Einheit ohne Menge", i+1)
		}
		out.Ingredients = append(out.Ingredients, item)
	}
	out.Steps = make([]Step, 0, len(in.Steps))
	for i, row := range in.Steps {
		instruction, textErr := cleanText(row.Instruction, 20000)
		if textErr != nil || instruction == "" || row.TimerSeconds < 0 || row.TimerSeconds > 86400 {
			return out, fmt.Errorf("schritt %d ist ungültig", i+1)
		}
		out.Steps = append(out.Steps, Step{Instruction: instruction, TimerSeconds: row.TimerSeconds})
	}
	out.Tags = make([]string, 0, len(in.Tags))
	seen := make(map[string]bool, len(in.Tags))
	for _, raw := range in.Tags {
		tag, textErr := cleanText(raw, 320)
		if textErr != nil || tag == "" || utf8.RuneCountInString(tag) > 80 {
			return out, errors.New("Rezept-Tag ist ungültig")
		}
		if !seen[strings.ToLower(tag)] {
			seen[strings.ToLower(tag)] = true
			out.Tags = append(out.Tags, tag)
		}
	}
	sort.Slice(out.Tags, func(i, j int) bool {
		left, right := strings.ToLower(out.Tags[i]), strings.ToLower(out.Tags[j])
		if left == right {
			return out.Tags[i] < out.Tags[j]
		}
		return left < right
	})
	out.Losses, out.SourceFormat = append([]string(nil), in.Losses...), in.SourceFormat
	return out, nil
}

func ParseHousehold(raw []byte) ([]Recipe, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes || jsonDepth(raw) > 32 {
		return nil, errors.New("Household-Datei ist leer, zu groß oder zu tief verschachtelt")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file HouseholdFile
	if err := decoder.Decode(&file); err != nil {
		return nil, errors.New("Household-JSON ist ungültig")
	}
	if decoder.Decode(new(any)) != io.EOF || file.Format != "household-recipe" || file.Version != 1 ||
		len(file.Recipes) == 0 || len(file.Recipes) > MaxRecipes {
		return nil, errors.New("Household-Format oder Rezeptanzahl wird nicht unterstützt")
	}
	out := make([]Recipe, 0, len(file.Recipes))
	for _, recipe := range file.Recipes {
		recipe.SourceFormat = "household-v1"
		normalized, err := Normalize(recipe)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func ExportHousehold(recipes []Recipe) ([]byte, error) {
	if len(recipes) == 0 || len(recipes) > MaxRecipes {
		return nil, errors.New("rezeptanzahl wird nicht unterstützt")
	}
	cleaned := make([]Recipe, 0, len(recipes))
	for _, recipe := range recipes {
		normalized, err := Normalize(recipe)
		if err != nil {
			return nil, err
		}
		cleaned = append(cleaned, normalized)
	}
	encoded, err := json.Marshal(HouseholdFile{Format: "household-recipe", Version: 1, Recipes: cleaned})
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxInputBytes {
		return nil, errors.New("Household-Datei ist größer als 2 MiB")
	}
	return encoded, nil
}

// PortableDigest intentionally ignores database IDs, original import source
// and loss diagnostics. It compares the same whitelisted content on both sides.
func PortableDigest(recipe Recipe) (string, error) {
	normalized, err := Normalize(recipe)
	if err != nil {
		return "", err
	}
	return hashPortable(normalized)
}

// ContentDigest keeps an existing database recipe editable when an older,
// otherwise valid field exceeds the narrower import/export profile. New
// imports still use PortableDigest and must pass the strict profile.
func ContentDigest(recipe Recipe) (string, error) {
	if normalized, err := Normalize(recipe); err == nil {
		return hashPortable(normalized)
	}
	recipe.Tags = append([]string(nil), recipe.Tags...)
	sort.Slice(recipe.Tags, func(i, j int) bool {
		left, right := strings.ToLower(recipe.Tags[i]), strings.ToLower(recipe.Tags[j])
		if left == right {
			return recipe.Tags[i] < recipe.Tags[j]
		}
		return left < right
	})
	return hashPortable(recipe)
}

func hashPortable(normalized Recipe) (string, error) {
	normalized.Losses, normalized.SourceFormat = nil, ""
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
