package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"time"

	"household/internal/value"
)

const pantryWasteLimit = 10000
const PantryWastePageSize = 50

type PantryWasteScope struct {
	Generation int64     `json:"generation"`
	Timezone   string    `json:"timezone"`
	Today      string    `json:"today"`
	Now        time.Time `json:"now"`
}

type PantryWasteFilter struct {
	From       string `json:"from"`
	To         string `json:"to"`
	ProductID  string `json:"product_id"`
	LocationID string `json:"location_id"`
	Reason     string `json:"reason"`
}

type PantryWastePoint struct {
	Created     time.Time `json:"created"`
	ID          string    `json:"id"`
	AsOf        time.Time `json:"as_of"`
	Fingerprint string    `json:"fingerprint"`
}

type PantryWasteRow struct {
	ID              string    `json:"id"`
	LotID           string    `json:"lot_id"`
	ProductID       string    `json:"product_id"`
	ProductName     string    `json:"product_name"`
	ProductVersion  int64     `json:"product_version"`
	LocationID      string    `json:"location_id"`
	Location        string    `json:"location"`
	LocationVersion int64     `json:"location_version"`
	CreatedAt       time.Time `json:"created_at"`
	Day             string    `json:"day"`
	Kind            string    `json:"kind"`
	Reason          string    `json:"reason"`
	Unit            string    `json:"unit"`
	Quantity        string    `json:"quantity"`
	SourceID        string    `json:"source_id"`
	ReversedBy      string    `json:"reversed_by"`
}

type PantryWasteTotal struct {
	Reason             string  `json:"reason,omitempty"`
	Day                string  `json:"day,omitempty"`
	Unit               string  `json:"unit"`
	Waste              string  `json:"waste"`
	Reversals          string  `json:"reversals"`
	Net                string  `json:"net"`
	Movements          int     `json:"movements"`
	EstimatedCostMinor *string `json:"estimated_cost_minor"`
}

type PantryWasteReport struct {
	Context            PantryWasteScope   `json:"context"`
	Filter             PantryWasteFilter  `json:"filter"`
	AsOf               time.Time          `json:"as_of"`
	Totals             []PantryWasteTotal `json:"totals"`
	Reasons            []PantryWasteTotal `json:"reasons"`
	Days               []PantryWasteTotal `json:"days"`
	Records            []PantryWasteRow   `json:"records"`
	Fingerprint        string             `json:"fingerprint"`
	PotentialRows      int                `json:"potential_rows"`
	Movements          int                `json:"movements"`
	UnknownReasons     int                `json:"unknown_reasons"`
	EstimatedCostMinor *string            `json:"estimated_cost_minor"`
	CostStatus         string             `json:"cost_status"`
	Next               *PantryWastePoint  `json:"-"`
}

type PantryWasteInput struct {
	Generation      int64  `json:"generation"`
	Action          string `json:"action"`
	LotVersion      int64  `json:"lot_version"`
	ProductVersion  int64  `json:"product_version"`
	LocationVersion int64  `json:"location_version"`
	MovementCount   int64  `json:"movement_count"`
	Balance         string `json:"balance"`
	Quantity        string `json:"quantity"`
	Reason          string `json:"reason"`
	MovementID      string `json:"movement_id"`
}

var pantryWasteReasons = map[string]string{
	"unknown": "Nicht bekannt / nicht angegeben", "expired": "Verbrauchsdatum überschritten",
	"spoiled": "Verdorben", "damaged": "Beschädigt", "overbought": "Zu viel eingekauft",
	"preparation": "Bei der Zubereitung übrig", "other": "Anderer Grund",
}

func PantryWasteReasons() []Record {
	result := make([]Record, 0, len(pantryWasteReasons))
	for _, code := range []string{"unknown", "expired", "spoiled", "damaged", "overbought", "preparation", "other"} {
		result = append(result, Record{"code": code, "label": pantryWasteReasons[code]})
	}
	return result
}

func NormalizePantryWasteFilter(in PantryWasteFilter, today string) (PantryWasteFilter, error) {
	if !value.Date(today) {
		return in, Invalid{"Haushaltsdatum erneut laden"}
	}
	if in.To == "" {
		in.To = today
	}
	if !value.Date(in.To) || in.To > today {
		return in, Invalid{"Bis-Datum muss ein gültiger Tag bis heute sein"}
	}
	end, _ := time.Parse(time.DateOnly, in.To)
	if in.From == "" {
		in.From = end.AddDate(0, 0, -29).Format(time.DateOnly)
		if in.From < "1900-01-01" {
			in.From = "1900-01-01"
		}
	}
	start, err := time.Parse(time.DateOnly, in.From)
	if err != nil || !value.Date(in.From) || in.From > in.To || (end.Unix()-start.Unix())/86400+1 > 366 {
		return in, Invalid{"Zeitraum mit höchstens 366 Kalendertagen auswählen"}
	}
	if in.ProductID != "" && !value.ValidUUID(in.ProductID) || in.LocationID != "" && !value.ValidUUID(in.LocationID) {
		return in, Invalid{"Produkt und Lagerort erneut auswählen"}
	}
	in.ProductID, in.LocationID = strings.ToLower(in.ProductID), strings.ToLower(in.LocationID)
	if in.Reason != "" && pantryWasteReasons[in.Reason] == "" {
		return in, Invalid{"Verlustgrund erneut auswählen"}
	}
	return in, nil
}

type wasteAccumulator struct {
	waste, reversals *big.Rat
	count            int
}

func summarizePantryWaste(rows []PantryWasteRow, point PantryWastePoint, sourceFingerprint string) (PantryWasteReport, error) {
	out := PantryWasteReport{Records: []PantryWasteRow{}, Totals: []PantryWasteTotal{}, Reasons: []PantryWasteTotal{}, Days: []PantryWasteTotal{}, CostStatus: "no_valuation_source"}
	if len(rows) > pantryWasteLimit {
		return out, Invalid{"Mehr als 10000 mögliche Verlust- oder Gegenbuchungen. Zeitraum oder Auswahl enger begrenzen; es wurden keine Teilsummen berechnet."}
	}
	rows = append([]PantryWasteRow(nil), rows...)
	sort.Slice(rows, func(a, b int) bool {
		if !rows[a].CreatedAt.Equal(rows[b].CreatedAt) {
			return rows[a].CreatedAt.After(rows[b].CreatedAt)
		}
		return rows[a].ID > rows[b].ID
	})
	out.Fingerprint = sourceFingerprint
	if out.Fingerprint == "" {
		encoded, err := json.Marshal(rows)
		if err != nil {
			return out, err
		}
		hash := sha256.Sum256(encoded)
		out.Fingerprint = hex.EncodeToString(hash[:])
	}
	if point.Fingerprint != "" && point.Fingerprint != out.Fingerprint {
		return out, ErrConflict
	}
	groups := []map[string]*wasteAccumulator{{}, {}, {}}
	for _, row := range rows {
		quantity, err := value.Decimal(row.Quantity)
		if err != nil || quantity.Sign() <= 0 || pantryWasteReasons[row.Reason] == "" || !pantryWasteUnit(row.Unit) || !value.Date(row.Day) || row.Kind != "waste" && row.Kind != "reversal" {
			return out, Invalid{"Gespeicherte Verlustbuchungen sind nicht eindeutig auswertbar; es wurden keine Teilsummen berechnet."}
		}
		if row.Reason == "unknown" {
			out.UnknownReasons++
		}
		for n, key := range []string{row.Unit, row.Reason + "/" + row.Unit, row.Day + "/" + row.Unit} {
			current := groups[n][key]
			if current == nil {
				current = &wasteAccumulator{waste: new(big.Rat), reversals: new(big.Rat)}
				groups[n][key] = current
			}
			if row.Kind == "waste" {
				current.waste.Add(current.waste, quantity)
			} else {
				current.reversals.Add(current.reversals, quantity)
			}
			current.count++
		}
	}
	for n, group := range groups {
		keys := make([]string, 0, len(group))
		for key := range group {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			current := group[key]
			parts := strings.SplitN(key, "/", 2)
			total := PantryWasteTotal{Unit: parts[len(parts)-1], Waste: current.waste.FloatString(6), Reversals: current.reversals.FloatString(6), Net: new(big.Rat).Sub(current.waste, current.reversals).FloatString(6), Movements: current.count}
			switch n {
			case 0:
				out.Totals = append(out.Totals, total)
			case 1:
				total.Reason = parts[0]
				out.Reasons = append(out.Reasons, total)
			case 2:
				total.Day = parts[0]
				out.Days = append(out.Days, total)
			}
		}
	}
	out.Movements = len(rows)
	for _, row := range rows {
		if point.ID != "" && (row.CreatedAt.After(point.Created) || row.CreatedAt.Equal(point.Created) && row.ID >= point.ID) {
			continue
		}
		out.Records = append(out.Records, row)
		if len(out.Records) > PantryWastePageSize {
			last := out.Records[PantryWastePageSize-1]
			out.Next = &PantryWastePoint{Created: last.CreatedAt, ID: last.ID, AsOf: point.AsOf, Fingerprint: out.Fingerprint}
			out.Records = out.Records[:PantryWastePageSize]
			break
		}
	}
	return out, nil
}

func pantryWasteUnit(unit string) bool {
	return unit == "piece" || unit == "portion" || unit == "g" || unit == "kg" || unit == "ml" || unit == "l"
}

// The proof is internal operation evidence, not part of public journal records.
func pantryMovementRecordJSON(mid, entity string) string {
	if mid == "pantry" && entity == "stock_movements" {
		return "to_jsonb(t)-ARRAY['waste_operation_key','waste_generation','waste_request_hash','waste_result_hash']"
	}
	return "to_jsonb(t)"
}
