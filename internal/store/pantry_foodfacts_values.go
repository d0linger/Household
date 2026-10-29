package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/foodfacts"
	"household/internal/value"
)

const pantryFoodFactsNamespace = "pantry-foodfacts-v1"

func foodFactsText(text string, limit int) bool {
	if !utf8.ValidString(text) || len(text) > limit*4 || utf8.RuneCountInString(text) > limit {
		return false
	}
	for _, char := range text {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func foodFactsProvider(provider string) bool {
	return provider == "off-production" || provider == "off-staging"
}

func foodFactsUnit(unit string) bool {
	return slices.Contains([]string{"piece", "g", "kg", "ml", "l", "portion"}, unit)
}

func foodFactsHex(raw string) bool {
	bytes, err := hex.DecodeString(raw)
	return err == nil && len(bytes) == 32 && len(raw) == 64
}

func normalizePantryFoodFacts(in PantryFoodFactsInput) (PantryFoodFactsInput, error) {
	if in.Generation < 1 || len(in.Code) > 256 {
		return in, Invalid{"Aktuelle Vorratsgeneration und Produktcode erneut prüfen"}
	}
	in.Code = strings.TrimSpace(in.Code)
	if _, err := foodfacts.ValidateGTIN(in.Code); err != nil || !foodFactsProvider(in.Provider) || !foodFactsHex(in.CacheFingerprint) {
		return in, Invalid{"Aktuellen externen Produktvorschlag erneut laden"}
	}
	in.CacheFingerprint = strings.ToLower(in.CacheFingerprint)
	if len(in.SelectedFields) < 1 || len(in.SelectedFields) > 3 {
		return in, Invalid{"Mindestens ein vorhandenes Quellenfeld ausdrücklich auswählen"}
	}
	in.SelectedFields = append([]string(nil), in.SelectedFields...)
	slices.Sort(in.SelectedFields)
	for index, field := range in.SelectedFields {
		if !slices.Contains([]string{"name", "brand", "pack_description"}, field) || index > 0 && field == in.SelectedFields[index-1] {
			return in, Invalid{"Quellenfelder eindeutig auswählen"}
		}
	}
	in.ProductID = strings.ToLower(in.ProductID)
	in.ProductName, in.PackLabel = strings.TrimSpace(in.ProductName), strings.TrimSpace(in.PackLabel)
	if in.ProductID == "" {
		if in.ProductVersion != 0 || in.ProductName == "" || !foodFactsText(in.ProductName, 200) || !foodFactsUnit(in.Unit) {
			return in, Invalid{"Neues Produkt mit eigenem Namen und ausdrücklicher Basiseinheit prüfen"}
		}
	} else if !value.ValidUUID(in.ProductID) || in.ProductVersion <= 0 || in.ProductName != "" || in.Unit != "" || in.IntegerQuantities {
		return in, Invalid{"Vorhandenes Produkt nur mit aktuellem Versionsstand auswählen; Stammdaten werden nicht überschrieben"}
	}
	if !in.CreatePack {
		if in.PackLabel != "" || in.PackQuantity != "" || in.PackUnit != "" {
			return in, Invalid{"Packungsanlage ausdrücklich auswählen oder Packungsangaben leeren"}
		}
	} else {
		if in.PackLabel == "" || !foodFactsText(in.PackLabel, 100) || len(in.PackQuantity) > 40 || !foodFactsUnit(in.PackUnit) {
			return in, Invalid{"Neue Packung mit Bezeichnung, exaktem Inhalt und Einheit prüfen"}
		}
		quantity, err := value.Decimal(in.PackQuantity)
		if err != nil {
			return in, Invalid{"Packungsinhalt als exakte positive Menge eingeben"}
		}
		in.PackQuantity, err = pantryExactAmount(quantity, false)
		if err != nil {
			return in, err
		}
	}
	return in, nil
}

func foodFactsHash(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func foodFactsRequestHash(id auth.Identity, key string, input PantryFoodFactsInput) ([]byte, error) {
	return foodFactsHash([]any{pantryFoodFactsNamespace, id.HouseholdID, id.UserID, key, input})
}

func foodFactsURL(provider, code string) string {
	host := "world.openfoodfacts.org"
	if provider == "off-staging" {
		host = "world.openfoodfacts.net"
	}
	return "https://" + host + "/product/" + code
}

type foodFactsCache struct {
	Household, Provider, Code, State, Name, Brand, Pack, Error string
	Generation                                                 int64
	Payload, Fingerprint                                       []byte
	FetchedAt, ExpiresAt                                       time.Time
	RetryAt                                                    *time.Time
}

func (cache foodFactsCache) digest() ([]byte, error) {
	retry := ""
	if cache.RetryAt != nil {
		retry = cache.RetryAt.UTC().Format(time.RFC3339Nano)
	}
	return foodFactsHash([]any{pantryFoodFactsNamespace + "/cache", cache.Household, cache.Generation,
		cache.Provider, cache.Code, cache.State, cache.Name, cache.Brand, cache.Pack, cache.Error,
		hex.EncodeToString(cache.Payload), cache.FetchedAt.UTC().Format(time.RFC3339Nano), cache.ExpiresAt.UTC().Format(time.RFC3339Nano), retry})
}

func (cache foodFactsCache) valid() bool {
	if !value.ValidUUID(cache.Household) || cache.Generation < 1 || !foodFactsProvider(cache.Provider) ||
		!foodFactsText(cache.Name, 200) || !foodFactsText(cache.Brand, 300) || !foodFactsText(cache.Pack, 500) ||
		!foodFactsText(cache.Error, 40) || cache.FetchedAt.IsZero() || !cache.ExpiresAt.After(cache.FetchedAt) ||
		len(cache.Payload) != 0 && len(cache.Payload) != 32 {
		return false
	}
	if _, err := foodfacts.ValidateGTIN(cache.Code); err != nil || !slices.Contains([]string{"found", "not_found", "error", "limited"}, cache.State) {
		return false
	}
	if cache.State != "found" && (cache.Name != "" || cache.Brand != "" || cache.Pack != "") {
		return false
	}
	if cache.State == "found" && (len(cache.Payload) != 32 || cache.Name == "" && cache.Brand == "" && cache.Pack == "") {
		return false
	}
	maximum := time.Minute
	switch cache.State {
	case "found":
		maximum = 7 * 24 * time.Hour
	case "not_found":
		maximum = time.Hour
	case "limited":
		maximum = 24 * time.Hour
	}
	if cache.ExpiresAt.Sub(cache.FetchedAt) > maximum {
		return false
	}
	digest, err := cache.digest()
	return err == nil && len(cache.Fingerprint) == 32 && bytes.Equal(digest, cache.Fingerprint)
}

func validFoodFactsSource(source PantryFoodFactsSource) bool {
	if !foodFactsProvider(source.Provider) || source.URL != foodFactsURL(source.Provider, source.Code) ||
		source.DatabaseLicense != "ODbL-1.0" || source.ContentsLicense != "DbCL-1.0" || !foodFactsHex(source.PayloadSHA256) ||
		!foodFactsHex(source.Fingerprint) || !foodFactsText(source.Name, 200) || !foodFactsText(source.Brand, 300) || !foodFactsText(source.PackDescription, 500) {
		return false
	}
	if _, err := foodfacts.ValidateGTIN(source.Code); err != nil {
		return false
	}
	fetched, first := time.Parse(time.RFC3339Nano, source.FetchedAt)
	expires, second := time.Parse(time.RFC3339Nano, source.ExpiresAt)
	return first == nil && second == nil && expires.After(fetched) && expires.Sub(fetched) <= 7*24*time.Hour
}

func (cache foodFactsCache) source() PantryFoodFactsSource {
	return PantryFoodFactsSource{Provider: cache.Provider, Code: cache.Code, Name: cache.Name, Brand: cache.Brand,
		PackDescription: cache.Pack, URL: foodFactsURL(cache.Provider, cache.Code), DatabaseLicense: "ODbL-1.0", ContentsLicense: "DbCL-1.0",
		FetchedAt: cache.FetchedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: cache.ExpiresAt.UTC().Format(time.RFC3339Nano),
		PayloadSHA256: hex.EncodeToString(cache.Payload), Fingerprint: hex.EncodeToString(cache.Fingerprint)}
}

func foodFactsDecode(raw []byte, target any, limit int) error {
	if len(raw) == 0 || len(raw) > limit {
		return ErrConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrConflict
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrConflict
	}
	return nil
}
