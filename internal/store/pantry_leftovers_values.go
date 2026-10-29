package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"slices"
	"strings"
	"unicode/utf8"

	"household/internal/value"
)

type PantryLeftoverContainer struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Quantity        string `json:"quantity"`
	LocationID      string `json:"location_id"`
	LocationVersion int64  `json:"location_version"`
	ReceivedOn      string `json:"received_on"`
	BestBeforeOn    string `json:"best_before_on"`
	UseByOn         string `json:"use_by_on"`
}

type PantryLeftoversInput struct {
	Generation        int64                     `json:"generation"`
	RecipesGeneration int64                     `json:"recipes_generation"`
	SourceKind        string                    `json:"source_kind"`
	SourceID          string                    `json:"source_id"`
	SourceVersionID   string                    `json:"source_version_id"`
	ProductID         string                    `json:"product_id"`
	ProductVersion    int64                     `json:"product_version"`
	Name              string                    `json:"name"`
	PreparedOn        string                    `json:"prepared_on"`
	PreparedPortions  string                    `json:"prepared_portions"`
	PortionsUnknown   bool                      `json:"portions_unknown"`
	Containers        []PantryLeftoverContainer `json:"containers"`
	Fingerprint       string                    `json:"fingerprint"`
}

func normalizePantryLeftovers(in PantryLeftoversInput) (PantryLeftoversInput, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.SourceID, in.SourceVersionID = strings.ToLower(in.SourceID), strings.ToLower(in.SourceVersionID)
	in.ProductID = strings.ToLower(in.ProductID)
	in.Fingerprint = strings.ToLower(in.Fingerprint)
	if in.Generation < 1 || !value.Date(in.PreparedOn) || !utf8.ValidString(in.Name) ||
		utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 200 || strings.ContainsRune(in.Name, 0) {
		return in, "", Invalid{"Gericht, Zubereitungstag und aktuellen Vorratsstand ausdrücklich angeben"}
	}
	if !slices.Contains([]string{"manual", "recipe_version", "cooking"}, in.SourceKind) {
		return in, "", Invalid{"Manuelle Zubereitung, Rezeptversion oder abgeschlossenen Kochvorgang auswählen"}
	}
	if in.SourceKind == "manual" {
		if in.SourceID != "" || in.SourceVersionID != "" || in.RecipesGeneration != 0 {
			return in, "", Invalid{"Manuelle Reste enthalten keinen fremden Rezeptverweis"}
		}
	} else if !value.ValidUUID(in.SourceID) || in.RecipesGeneration < 1 ||
		in.SourceVersionID != "" && !value.ValidUUID(in.SourceVersionID) {
		return in, "", Invalid{"Aktuellen Rezept- oder Kochbezug erneut auswählen"}
	}
	if in.ProductID != "" && (!value.ValidUUID(in.ProductID) || in.ProductVersion < 1) ||
		in.ProductID == "" && in.ProductVersion != 0 {
		return in, "", Invalid{"Vorhandenes Portionsprodukt mit aktuellem Versionsstand auswählen"}
	}
	if in.Fingerprint != "" {
		if decoded, err := hex.DecodeString(in.Fingerprint); err != nil || len(decoded) != sha256.Size {
			return in, "", Invalid{"Gesamtprüfung erneut laden"}
		}
	}
	if len(in.Containers) < 1 || len(in.Containers) > 20 {
		return in, "", Invalid{"Eine bis zwanzig tatsächliche Restebehälter gemeinsam prüfen"}
	}
	in.Containers = slices.Clone(in.Containers)
	seen := map[string]bool{}
	total := new(big.Rat)
	for i := range in.Containers {
		line := &in.Containers[i]
		line.ID, line.LocationID = strings.ToLower(line.ID), strings.ToLower(line.LocationID)
		line.Label = strings.TrimSpace(line.Label)
		if !value.ValidUUID(line.ID) || seen[line.ID] || !value.ValidUUID(line.LocationID) || line.LocationVersion < 1 ||
			!utf8.ValidString(line.Label) || utf8.RuneCountInString(line.Label) < 1 || utf8.RuneCountInString(line.Label) > 200 || strings.ContainsRune(line.Label, 0) {
			return in, "", Invalid{"Jeden Behälter eindeutig benennen und einen geprüften Lagerort auswählen"}
		}
		seen[line.ID] = true
		if !value.Date(line.ReceivedOn) || line.ReceivedOn < in.PreparedOn ||
			line.BestBeforeOn != "" && (!value.Date(line.BestBeforeOn) || line.BestBeforeOn < in.PreparedOn) ||
			line.UseByOn != "" && (!value.Date(line.UseByOn) || line.UseByOn < in.PreparedOn) {
			return in, "", Invalid{"Zubereitung, Einlagerung und freiwillige Haltbarkeitsdaten ausdrücklich prüfen"}
		}
		q, err := value.Decimal(line.Quantity)
		if err != nil || q.Sign() <= 0 {
			return in, "", Invalid{"Tatsächlich übrige Portionen als positive exakte Menge angeben"}
		}
		line.Quantity, err = pantryExactAmount(q, false)
		if err != nil {
			return in, "", err
		}
		total.Add(total, q)
	}
	amount, err := pantryExactAmount(total, false)
	if err != nil {
		return in, "", err
	}
	if in.PreparedPortions != "" {
		cap, err := value.Decimal(in.PreparedPortions)
		if err != nil || cap.Sign() <= 0 || in.PortionsUnknown {
			return in, "", Invalid{"Bekannte zubereitete Gesamtportionen oder ausdrücklich unbekannte Gesamtmenge auswählen"}
		}
		in.PreparedPortions, err = pantryExactAmount(cap, false)
		if err != nil {
			return in, "", err
		}
		if total.Cmp(cap) > 0 {
			return in, "", Invalid{"Gemeinsame Restportionen überschreiten die bekannte zubereitete Menge"}
		}
	} else if in.SourceKind != "cooking" && !in.PortionsUnknown {
		return in, "", Invalid{"Unbekannte zubereitete Gesamtmenge ausdrücklich kennzeichnen"}
	}
	return in, amount, nil
}

func pantryLeftoversDigest(in PantryLeftoversInput) ([]byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(raw) > 128<<10 {
		return nil, Invalid{"Resteeinlagerung überschreitet die zulässige Größe"}
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}
