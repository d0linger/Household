package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/finance"
)

type FinanceBankRow struct {
	RowNumber   int    `json:"row_number"`
	Fingerprint string `json:"fingerprint"`
	OccurredOn  string `json:"occurred_on"`
	Payee       string `json:"payee"`
	Description string `json:"description"`
	Currency    string `json:"currency"`
	Exponent    int    `json:"exponent"`
	AmountMinor string `json:"amount_minor"`
	ExternalID  string `json:"external_id"`
	Reference   string `json:"reference"`
	Duplicate   bool   `json:"duplicate"`
	Similar     bool   `json:"similar"`
	Error       string `json:"error"`
}

type FinanceBankDecision struct {
	RowNumber   int     `json:"row_number"`
	Fingerprint string  `json:"fingerprint"`
	Decision    string  `json:"decision"`
	CategoryID  *string `json:"category_id"`
}

type FinanceBankConfirmInput struct {
	Generation    int64                 `json:"generation"`
	Version       int64                 `json:"version"`
	AccessVersion string                `json:"access_version"`
	Decisions     []FinanceBankDecision `json:"decisions"`
}

func validateFinanceBankProfile(in FinanceBankProfileInput) error {
	if !financeText(in.Name, 1, 100) || !slices.Contains([]string{",", ";", "\t"}, in.Delimiter) || in.Encoding != "utf-8" ||
		!slices.Contains([]string{"iso", "dmy", "slash_dmy", "slash_mdy"}, in.DateLayout) ||
		!slices.Contains([]string{".", ","}, in.DecimalSeparator) || !slices.Contains([]string{"", ".", ",", " "}, in.GroupSeparator) ||
		in.DecimalSeparator == in.GroupSeparator || !slices.Contains([]string{"signed", "debit_credit"}, in.AmountMode) {
		return Invalid{"Dokumentiertes UTF-8-Bankprofil mit eindeutigen Trennzeichen auswählen"}
	}
	f := in.FieldMap
	for _, name := range []string{f.Date, f.Amount, f.Debit, f.Credit, f.Currency, f.Payee, f.Description, f.Reference, f.ExternalID} {
		if !financeText(name, 0, 100) {
			return Invalid{"Spaltennamen des Bankprofils prüfen"}
		}
	}
	if f.Date == "" || in.AmountMode == "signed" && (f.Amount == "" || f.Debit != "" || f.Credit != "") ||
		in.AmountMode == "debit_credit" && (f.Amount != "" || f.Debit == "" || f.Credit == "" || f.Debit == f.Credit) {
		return Invalid{"Datums- und Betragszuordnung vollständig und eindeutig angeben"}
	}
	return nil
}

func financeParseBankCSV(raw []byte, profile FinanceBankProfileInput, account, currency string, exponent int) ([]FinanceBankRow, string, error) {
	if len(raw) == 0 || len(raw) > 1<<20 || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, "", Invalid{"Bankdatei muss begrenztes UTF-8 ohne Nullbytes sein"}
	}
	if err := validateFinanceBankProfile(profile); err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	sourceHash := hex.EncodeToString(digest[:])
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.Comma = []rune(profile.Delimiter)[0]
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil || len(headers) == 0 || len(headers) > 64 {
		return nil, "", Invalid{"Gültige Bank-Kopfzeile mit höchstens64Spalten erforderlich"}
	}
	indices := map[string]int{}
	for index, header := range headers {
		header = strings.TrimSpace(header)
		if !financeText(header, 1, 100) {
			return nil, "", Invalid{"Leere oder übergroße Bank-Spaltenüberschrift"}
		}
		if _, exists := indices[header]; exists {
			return nil, "", Invalid{"Doppelte Bank-Spaltenüberschrift"}
		}
		indices[header] = index
	}
	f := profile.FieldMap
	for _, name := range []string{f.Date, f.Amount, f.Debit, f.Credit, f.Currency, f.Payee, f.Description, f.Reference, f.ExternalID} {
		if name != "" {
			if _, exists := indices[name]; !exists {
				return nil, "", Invalid{"Das ausgewählte Bankprofil passt nicht zur Kopfzeile"}
			}
		}
	}
	rows := []FinanceBankRow{}
	seen := map[string]bool{}
	for {
		fields, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, "", Invalid{"CSV-Struktur ist beschädigt; es wurde kein Import begonnen"}
		}
		if len(rows) >= 500 {
			return nil, "", Invalid{"Bankdatei auf höchstens500Datenzeilen begrenzen"}
		}
		row := FinanceBankRow{RowNumber: len(rows) + 1, Currency: currency, Exponent: exponent}
		if len(fields) != len(headers) {
			row.Error = "Spaltenzahl stimmt nicht mit der Kopfzeile überein"
		}
		get := func(name string) string {
			if name == "" {
				return ""
			}
			index := indices[name]
			if index >= len(fields) {
				return ""
			}
			return strings.TrimSpace(fields[index])
		}
		for _, field := range fields {
			if len(field) > 4096 {
				row.Error = "Eine Bankzelle ist zu groß"
			}
		}
		layout := map[string]string{"iso": "2006-01-02", "dmy": "02.01.2006", "slash_dmy": "02/01/2006", "slash_mdy": "01/02/2006"}[profile.DateLayout]
		date, dateErr := time.Parse(layout, get(f.Date))
		if dateErr != nil || date.Format(layout) != get(f.Date) || !finance.CivilDate(date.Format(time.DateOnly)) {
			row.Error = "Datum entspricht nicht dem gewählten Profil"
		} else {
			row.OccurredOn = date.Format(time.DateOnly)
		}
		if f.Currency != "" && get(f.Currency) != currency {
			row.Error = "Währung stimmt nicht mit dem ausgewählten Konto überein"
		}
		var amount int64
		var amountErr error
		if profile.AmountMode == "signed" {
			amount, amountErr = financeParseBankDecimal(get(f.Amount), exponent, profile.DecimalSeparator, profile.GroupSeparator)
		} else {
			debit, credit := int64(0), int64(0)
			if get(f.Debit) != "" {
				debit, amountErr = financeParseBankDecimal(get(f.Debit), exponent, profile.DecimalSeparator, profile.GroupSeparator)
			}
			if get(f.Credit) != "" {
				var e error
				credit, e = financeParseBankDecimal(get(f.Credit), exponent, profile.DecimalSeparator, profile.GroupSeparator)
				if e != nil {
					amountErr = e
				}
			}
			if debit < 0 || credit < 0 || debit > 0 && credit > 0 {
				amountErr = finance.ErrMoney
			}
			amount = credit - debit
		}
		if amountErr != nil || amount == 0 {
			row.Error = "Betrag/Vorzeichen entspricht nicht dem Profil oder ist null"
		} else {
			row.AmountMinor = strconv.FormatInt(amount, 10)
		}
		row.Payee, row.Description, row.Reference, row.ExternalID = get(f.Payee), get(f.Description), get(f.Reference), get(f.ExternalID)
		if row.Description == "" {
			row.Description = row.Payee
		}
		if row.Description == "" {
			row.Description = "Bankbuchung"
		}
		if !financeText(row.Payee, 0, 200) || !financeText(row.Description, 1, 300) || !financeText(row.Reference, 0, 300) || !financeText(row.ExternalID, 0, 200) {
			row.Error = "Empfänger, Beschreibung oder Quellenkennung ist zu lang"
		}
		identity := []any{"bank-file-row-v1", account, sourceHash, row.RowNumber}
		if row.ExternalID != "" {
			identity = []any{"bank-transaction-v1", account, row.ExternalID}
		}
		identityBytes, marshalErr := json.Marshal(identity)
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		fingerprint := sha256.Sum256(identityBytes)
		row.Fingerprint = hex.EncodeToString(fingerprint[:])
		if seen[row.Fingerprint] {
			row.Duplicate = true
		}
		seen[row.Fingerprint] = true
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, "", Invalid{"Bankdatei enthält keine Datenzeilen"}
	}
	return rows, sourceHash, nil
}

// financeParseBankDecimal accepts only the declared decimal/group syntax.
// Grouping is validated before removal, so 12.34 is not silently read as1234.
func financeParseBankDecimal(raw string, exponent int, decimal, group string) (int64, error) {
	if raw == "" || len(raw) > 64 || exponent < 0 || exponent > 3 || decimal == group {
		return 0, finance.ErrMoney
	}
	negative := false
	if raw[0] == '-' || raw[0] == '+' {
		negative = raw[0] == '-'
		raw = raw[1:]
	}
	parts := strings.Split(raw, decimal)
	if len(parts) > 2 || len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > exponent) {
		return 0, finance.ErrMoney
	}
	whole := parts[0]
	if group != "" && strings.Contains(whole, group) {
		groups := strings.Split(whole, group)
		if len(groups[0]) < 1 || len(groups[0]) > 3 {
			return 0, finance.ErrMoney
		}
		for _, part := range groups[1:] {
			if len(part) != 3 {
				return 0, finance.ErrMoney
			}
		}
		whole = strings.Join(groups, "")
	}
	if whole == "" {
		return 0, finance.ErrMoney
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	fraction += strings.Repeat("0", exponent-len(fraction))
	digits := whole + fraction
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return 0, finance.ErrMoney
		}
	}
	amount, ok := new(big.Int).SetString(digits, 10)
	if !ok || !amount.IsInt64() || amount.Int64() > finance.MinorLimit {
		return 0, finance.ErrMoney
	}
	n := amount.Int64()
	if negative {
		n = -n
	}
	return n, nil
}

func financeBankSourceKind(row FinanceBankRow) string {
	if row.ExternalID != "" {
		return "bank_transaction"
	}
	return "bank_file_row"
}
