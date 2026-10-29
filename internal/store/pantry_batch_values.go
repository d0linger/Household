package store

import (
	"encoding/json"
	"math/big"
	"strings"
	"unicode/utf8"

	"household/internal/value"
)

type PantryBatchLine struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	ProductVersion  int64  `json:"product_version"`
	LocationID      string `json:"location_id"`
	LocationVersion int64  `json:"location_version"`
	LotID           string `json:"lot_id"`
	LotVersion      int64  `json:"lot_version"`
	MovementCount   int64  `json:"movement_count"`
	Balance         string `json:"balance"`
	Quantity        string `json:"quantity"`
	Unit            string `json:"unit"`
	PackID          string `json:"pack_id"`
	PackVersion     int64  `json:"pack_version"`
	Label           string `json:"label"`
	ReceivedOn      string `json:"received_on"`
	BestBefore      string `json:"best_before_on"`
	UseBy           string `json:"use_by_on"`
	OpenedOn        string `json:"opened_on"`
	AfterOpenDays   *int64 `json:"after_open_days"`
	AfterOpenSource string `json:"after_open_source"`
	AfterOpenNote   string `json:"after_open_note"`
	Reason          string `json:"reason"`
}

type PantryBatchDraftInput struct {
	Generation int64             `json:"generation"`
	Version    int64             `json:"version"`
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	LocationID string            `json:"location_id"`
	Lines      []PantryBatchLine `json:"lines"`
}

func batchText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// Incomplete draft fields remain input text. Booking validation is separate;
// saving progress must neither round quantities nor create physical effects.
func normalizePantryBatchDraft(in PantryBatchDraftInput) (PantryBatchDraftInput, error) {
	if in.Generation < 1 || in.Version < 0 || (in.Kind != "intake" && in.Kind != "inventory") || len(in.Lines) > 100 {
		return in, Invalid{"Gültiger Entwurfsstand und höchstens 100 Zeilen erforderlich"}
	}
	in.Title = strings.TrimSpace(in.Title)
	in.LocationID = strings.ToLower(in.LocationID)
	if !batchText(in.Title, 100) || (in.LocationID != "" && !value.ValidUUID(in.LocationID)) || in.Kind == "intake" && in.LocationID != "" {
		return in, Invalid{"Entwurfstitel oder Inventur-Lagerort ist ungültig"}
	}
	in.Lines = append([]PantryBatchLine{}, in.Lines...)
	seen, lots := map[string]bool{}, map[string]bool{}
	for i := range in.Lines {
		line := &in.Lines[i]
		line.ID, line.ProductID, line.LocationID = strings.ToLower(line.ID), strings.ToLower(line.ProductID), strings.ToLower(line.LocationID)
		line.LotID, line.PackID = strings.ToLower(line.LotID), strings.ToLower(line.PackID)
		if !value.ValidUUID(line.ID) || seen[line.ID] {
			return in, Invalid{"Jede Entwurfszeile braucht eine eindeutige Kennung"}
		}
		seen[line.ID] = true
		for _, ref := range []string{line.ProductID, line.LocationID, line.LotID, line.PackID} {
			if ref != "" && !value.ValidUUID(ref) {
				return in, Invalid{"Produkt-, Orts-, Chargen- oder Packungskennung ist ungültig"}
			}
		}
		if line.ProductVersion < 0 || line.LocationVersion < 0 || line.LotVersion < 0 || line.PackVersion < 0 || line.MovementCount < 0 {
			return in, Invalid{"Versionsstände und Bewegungszähler dürfen nicht negativ sein"}
		}
		if !batchText(line.Quantity, 40) || !batchText(line.Balance, 40) || !batchText(line.Label, 300) || !batchText(line.Reason, 500) || !batchText(line.AfterOpenNote, 500) {
			return in, Invalid{"Mengen oder Texte überschreiten die erlaubte Länge oder enthalten ungültige Zeichen"}
		}
		for _, date := range []string{line.ReceivedOn, line.BestBefore, line.UseBy, line.OpenedOn} {
			if !batchText(date, 10) {
				return in, Invalid{"Datumsentwurf auf zehn Zeichen begrenzen"}
			}
		}
		if line.Unit != "" && line.Unit != "piece" && line.Unit != "portion" && line.Unit != "g" && line.Unit != "kg" && line.Unit != "ml" && line.Unit != "l" {
			return in, Invalid{"Unterstützte Mengen- oder Packungseinheit auswählen"}
		}
		if line.AfterOpenSource != "" && line.AfterOpenSource != "user" && line.AfterOpenSource != "manufacturer" {
			return in, Invalid{"Öffnungshinweis mit gültiger Herkunft speichern"}
		}
		if line.AfterOpenDays != nil {
			if *line.AfterOpenDays < 0 || *line.AfterOpenDays > 3660 {
				return in, Invalid{"Öffnungshinweis: null bis 3660 Tage"}
			}
			days := *line.AfterOpenDays
			line.AfterOpenDays = &days
		}
		if in.Kind == "intake" && (line.LotID != "" || line.LotVersion != 0 || line.MovementCount != 0 || line.Balance != "") {
			return in, Invalid{"Einlagerung legt neue Chargen an; keine vorhandenen Bestandsstände übermitteln"}
		}
		if in.Kind == "inventory" {
			if line.PackID != "" || line.PackVersion != 0 || line.Label != "" || line.ReceivedOn != "" || line.BestBefore != "" || line.UseBy != "" || line.OpenedOn != "" || line.AfterOpenDays != nil || line.AfterOpenSource != "" || line.AfterOpenNote != "" {
				return in, Invalid{"Inventur zählt vorhandene Chargen und ändert keine Packungs- oder Chargenmetadaten"}
			}
			if line.LotID != "" {
				if lots[line.LotID] {
					return in, Invalid{"Eine Charge darf nur einmal gezählt werden"}
				}
				lots[line.LotID] = true
			}
			if in.LocationID != "" && line.LocationID != "" && line.LocationID != in.LocationID {
				return in, Invalid{"Alle Zählzeilen müssen zum ausgewählten Lagerort gehören"}
			}
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	if len(raw) > 1<<20 {
		return in, Invalid{"Entwurf überschreitet 1 MiB"}
	}
	return in, nil
}

// Projection/count values have a wider range than a single journal movement.
func pantryCountAmount(raw string, integers bool) (string, error) {
	q, err := stockDecimal(strings.ReplaceAll(strings.TrimSpace(raw), ",", "."))
	limit := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil))
	if err != nil || q.Sign() < 0 || q.Cmp(limit) >= 0 || integers && !q.IsInt() {
		return "", Invalid{"Nichtnegativen Zählbestand mit höchstens sechs Nachkommastellen und gegebenenfalls ganzen Basiseinheiten eingeben"}
	}
	return q.FloatString(6), nil
}

func pantryInventoryDelta(before, count string, integers bool) (string, error) {
	b, err := stockDecimal(before)
	if err != nil || b.Sign() < 0 {
		return "", Invalid{"Geprüften Buchbestand erneut laden"}
	}
	normalized, err := pantryCountAmount(count, integers)
	if err != nil {
		return "", err
	}
	c, _ := stockDecimal(normalized)
	delta := new(big.Rat).Sub(c, b)
	if delta.Sign() == 0 {
		return "0.000000", nil
	}
	abs := new(big.Rat).Abs(delta)
	encoded, err := pantryExactAmount(abs, integers)
	if err != nil {
		return "", Invalid{"Die Zähldifferenz ist nicht als einzelne exakte Bestandsbewegung buchbar. Einheit und gezählten Bestand prüfen."}
	}
	if delta.Sign() < 0 {
		encoded = "-" + encoded
	}
	return encoded, nil
}

func pantryIntakeAmount(quantity, unit, base string, pack Record, integers bool) (string, error) {
	q, err := value.Decimal(strings.ReplaceAll(strings.TrimSpace(quantity), ",", "."))
	if err != nil || q.Sign() <= 0 {
		return "", Invalid{"Positive tatsächliche Menge oder Packungsanzahl erforderlich"}
	}
	if pack != nil {
		p, parseErr := value.Decimal(String(pack["quantity"]))
		if parseErr != nil || p.Sign() <= 0 {
			return "", Invalid{"Geprüften positiven Packungsinhalt laden"}
		}
		q.Mul(q, p)
		unit = String(pack["unit_code"])
	}
	converted, err := value.Convert(q, unit, base)
	if err != nil {
		return "", Invalid{"Einlagerungs- und Produktbasiseinheit sind nicht kompatibel"}
	}
	return pantryExactAmount(converted, integers)
}
