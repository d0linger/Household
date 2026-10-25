package store

import (
	"math/big"

	"household/internal/value"
)

// PantryReplenishment is a current physical-stock calculation, not a reservation.
// All amounts are exact decimal strings in the product's base unit.
type PantryReplenishment struct {
	Reason       string `json:"reason"`
	Suggested    bool   `json:"suggested"`
	TargetSource string `json:"target_source"`
	Target       string `json:"target"`
	Shortage     string `json:"shortage"`
	Quantity     string `json:"quantity"`
	PackCount    string `json:"pack_count"`
	Surplus      string `json:"surplus"`
	After        string `json:"after"`
}

func validatePantryTarget(product Record) error {
	if product["target_stock"] == nil {
		return nil
	}
	target, err := value.Decimal(String(product["target_stock"]))
	if err != nil || target.Sign() < 0 {
		return Invalid{"Zielbestand muss eine nicht negative Menge sein"}
	}
	minimum, err := value.Decimal(String(product["low_stock_threshold"]))
	if err != nil || minimum.Sign() < 0 || target.Cmp(minimum) < 0 {
		return Invalid{"Zum Zielbestand einen Mindestbestand angeben; das Ziel darf nicht darunter liegen"}
	}
	if product["integer_quantities"] == true && !target.IsInt() {
		return Invalid{"Für dieses Produkt einen ganzen Zielbestand angeben"}
	}
	return nil
}

func pantryReplenishment(product Record, balance string, pack Record) (PantryReplenishment, error) {
	out := PantryReplenishment{Reason: "minimum_unknown", TargetSource: "minimum", Shortage: "0", Quantity: "0", Surplus: "0"}
	stock, err := stockDecimal(balance)
	if err != nil || stock.Sign() < 0 {
		return out, Invalid{"Physischen Bestand erneut prüfen"}
	}
	out.After = stock.FloatString(6)
	if err = validatePantryTarget(product); err != nil {
		return out, err
	}
	if product["low_stock_threshold"] == nil {
		return out, nil
	}
	minimum, err := value.Decimal(String(product["low_stock_threshold"]))
	if err != nil || minimum.Sign() < 0 {
		return out, Invalid{"Mindestbestand erneut prüfen"}
	}
	target := new(big.Rat).Set(minimum)
	if product["target_stock"] != nil {
		target, err = value.Decimal(String(product["target_stock"]))
		if err != nil {
			return out, err
		}
		out.TargetSource = "target"
	}
	out.Target = target.FloatString(6)
	out.Reason = "minimum_met"
	if product["archived_at"] != nil {
		out.Reason = "product_archived"
		return out, nil
	}
	if stock.Cmp(minimum) >= 0 {
		return out, nil
	}
	shortage := new(big.Rat).Sub(target, stock)
	amount := new(big.Rat).Set(shortage)
	if len(pack) > 0 {
		content, parseErr := value.Decimal(String(pack["quantity"]))
		if parseErr != nil || content.Sign() <= 0 || pack["disabled"] == true {
			return out, Invalid{"Eine aktive Packung mit positivem Inhalt auswählen"}
		}
		content, err = value.Convert(content, String(pack["unit_code"]), String(product["unit_code"]))
		if err != nil {
			return out, Invalid{"Packung und Basiseinheit sind nicht kompatibel"}
		}
		quotient := new(big.Rat).Quo(shortage, content)
		count, remainder := new(big.Int), new(big.Int)
		count.QuoRem(quotient.Num(), quotient.Denom(), remainder)
		if remainder.Sign() > 0 {
			count.Add(count, big.NewInt(1))
		}
		out.PackCount = count.String()
		amount.Mul(content, new(big.Rat).SetInt(count))
	}
	out.Quantity, err = pantryExactAmount(amount, product["integer_quantities"] == true)
	if err != nil {
		return out, err
	}
	out.Shortage = shortage.FloatString(6)
	out.Surplus = new(big.Rat).Sub(amount, shortage).FloatString(6)
	out.After = new(big.Rat).Add(stock, amount).FloatString(6)
	out.Reason, out.Suggested = "below_minimum", true
	return out, nil
}
