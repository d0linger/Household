package store

import (
	"math/big"
	"strings"
	"time"

	"household/internal/value"
)

type PantryStockInput struct {
	Generation      int64  `json:"generation"`
	Action          string `json:"action"`
	LotVersion      int64  `json:"lot_version"`
	ProductVersion  int64  `json:"product_version"`
	LocationVersion int64  `json:"location_version"`
	MovementCount   int64  `json:"movement_count"`
	Balance         string `json:"balance"`
	Mode            string `json:"mode"`
	Quantity        string `json:"quantity"`
	PackID          string `json:"pack_id"`
	PackVersion     int64  `json:"pack_version"`
	MovementID      string `json:"movement_id"`
}

type PantryStockConflict struct {
	Message string
	Current Record
}

func (e PantryStockConflict) Error() string { return e.Message }
func (e PantryStockConflict) Unwrap() error { return ErrConflict }

func pantryBarcode(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if len(code) == 0 || len(code) > 64 {
		return "", Invalid{"Barcode: 1 bis 64 sichtbare ASCII-Zeichen ohne Leerzeichen erforderlich"}
	}
	for _, b := range []byte(code) {
		if b < 33 || b > 126 {
			return "", Invalid{"Barcode enthält Leerzeichen, Steuerzeichen oder nicht unterstützte Zeichen"}
		}
	}
	return code, nil
}

func pantryExactAmount(amount *big.Rat, integers bool) (string, error) {
	if amount == nil || amount.Sign() <= 0 || integers && !amount.IsInt() {
		return "", Invalid{"Positive Menge erforderlich; dieses Produkt erlaubt gegebenenfalls nur ganze Basiseinheiten"}
	}
	encoded := amount.FloatString(6)
	checked, err := value.Decimal(encoded)
	if err != nil || checked.Sign() <= 0 || checked.Cmp(amount) != 0 {
		return "", Invalid{"Menge liegt außerhalb des Wertebereichs oder benötigt mehr als sechs Nachkommastellen. Menge oder Basiseinheit prüfen."}
	}
	return encoded, nil
}

func pantryConsumption(mode, quantity, balance, packAmount, packUnit, baseUnit string, integers bool) (string, error) {
	stock, err := stockDecimal(balance)
	if err != nil || stock.Sign() < 0 {
		return "", Invalid{"Gespeicherten Bestand erneut laden"}
	}
	var amount *big.Rat
	switch mode {
	case "half":
		amount = new(big.Rat).Quo(stock, big.NewRat(2, 1))
	case "amount", "pack":
		amount, err = value.Decimal(quantity)
		if err != nil || amount.Sign() <= 0 {
			return "", Invalid{"Positive Menge beziehungsweise Packungsanzahl erforderlich"}
		}
		if mode == "pack" {
			pack, parseErr := value.Decimal(packAmount)
			if parseErr != nil || pack.Sign() <= 0 {
				return "", Invalid{"Packungsinhalt erneut prüfen"}
			}
			amount, err = value.Convert(new(big.Rat).Mul(amount, pack), packUnit, baseUnit)
			if err != nil {
				return "", Invalid{"Packungs- und Basiseinheit sind nicht kompatibel"}
			}
		}
	default:
		return "", Invalid{"Eigene Menge, Hälfte oder Packung auswählen"}
	}
	encoded, err := pantryExactAmount(amount, integers)
	if err != nil {
		return "", err
	}
	if amount.Cmp(stock) > 0 {
		return "", Invalid{"Die geprüfte Verbrauchsmenge übersteigt den verfügbaren Chargenbestand"}
	}
	return encoded, nil
}

// Dates are calendar days, independent of DST offsets or browser timezone.
func pantryOpeningHint(opened string, days *int64) (string, error) {
	if opened == "" || days == nil {
		return "", nil
	}
	if !value.Date(opened) || *days < 0 || *days > 3660 {
		return "", Invalid{"Öffnungsdatum und Hinweis von 0 bis 3660 Tagen prüfen"}
	}
	at, err := time.Parse(time.DateOnly, opened)
	if err != nil {
		return "", err
	}
	return at.AddDate(0, 0, int(*days)).Format(time.DateOnly), nil
}

func pantryPriority(bestBefore, useBy, opening string) (string, string) {
	date, kind := "", "unknown"
	// A tie retains the more explicit use-by kind rather than relabelling it.
	for _, item := range []struct{ date, kind string }{{useBy, "use_by"}, {opening, "opening_hint"}, {bestBefore, "best_before"}} {
		if item.date != "" && (date == "" || item.date < date) {
			date, kind = item.date, item.kind
		}
	}
	return date, kind
}
