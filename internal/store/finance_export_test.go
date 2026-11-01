package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
)

func TestFinanceExportPresentationFormulaEscape(t *testing.T) {
	for _, test := range []struct{ name, raw, want string }{
		{"formula", "=SUM(A1:A2)", "'=SUM(A1:A2)"},
		{"unicode_space", "\u2007+1", "'\u2007+1"},
		{"text_negative", "-123", "'-123"},
		{"tab", "\tplain", "'\tplain"},
		{"newline_before_formula", "\n@SUM(1)", "'\n@SUM(1)"},
		{"normal_multiline", "Markt, Nord\nZweite Zeile", "Markt, Nord\nZweite Zeile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := financeExportText(test.raw)
			if err != nil || got != test.want {
				t.Fatal("untrusted text presentation escaping differs")
			}
		})
	}
	if _, err := financeExportText("invalid\x00text"); err == nil {
		t.Fatal("NUL accepted")
	}
	if got, err := financeExportMinor("-123"); err != nil || got != "-123" {
		t.Fatal("real signed integer was escaped like a formula")
	}
}

func TestFinanceExportExactMoneyUnknownAndDenomination(t *testing.T) {
	for _, test := range []struct {
		name  string
		input any
		want  string
		valid bool
	}{
		{"unknown", nil, "", true}, {"actual_zero", "0", "0", true}, {"negative", "-1", "-1", true}, {"large_exact_aggregate", "9007199254740993", "9007199254740993", true},
		{"number", 1, "", false}, {"json_number", json.Number("1"), "", false}, {"leading_zero", "01", "", false}, {"negative_zero", "-0", "", false}, {"decimal", "1.0", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := financeExportMinor(test.input)
			if (err == nil) != test.valid || got != test.want {
				t.Fatal("money was coerced or unknown became zero")
			}
		})
	}
	for _, test := range []struct {
		name, code string
		exponent   any
		valid      bool
	}{
		{"yen", "JPY", 0, true}, {"euro", "EUR", json.Number("2"), true}, {"dinar", "KWD", 3, true}, {"wrong_units", "EUR", 3, false}, {"unknown_code", "XYZ", 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := financeExportDenomination(test.code, test.exponent)
			if (err == nil) != test.valid {
				t.Fatal("currency/exponent pair invented")
			}
		})
	}
}

func TestFinanceExportFlatCSVPreservesSourceFacts(t *testing.T) {
	file, err := newFinanceExportCSV(financialScope{generation: 2, readAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}, FinanceQuery{Pane: "entries"})
	if err != nil {
		t.Fatal(err)
	}
	record := Record{"id": "00000000-0000-4000-8000-000000000001", "version": int64(1), "description": "=FORMULA,\nOriginaltext", "kind": "expense", "state": "draft", "money": Record{"currency": "KWD", "exponent": 3, "minor": "1", "base_minor": nil, "original_status": "known", "base_status": "rate_missing"}}
	if err = file.append("entry", financeExportFlatten("entry", record)); err != nil {
		t.Fatal(err)
	}
	record["money"].(Record)["base_minor"] = "0"
	record["money"].(Record)["base_status"] = "below_base_precision"
	if err = file.append("entry", financeExportFlatten("entry", record)); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(file.buffer.Bytes())).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatal("CSV did not preserve quoted logical rows")
	}
	column := func(key string) int {
		t.Helper()
		for i, name := range rows[0] {
			if name == key {
				return i
			}
		}
		t.Fatal("missing typed column", key)
		return -1
	}
	if rows[1][column("base_minor")] != "" || rows[2][column("base_minor")] != "0" || rows[1][column("original_minor")] != "1" || rows[1][column("description")] != "'=FORMULA,\nOriginaltext" || record["description"] != "=FORMULA,\nOriginaltext" {
		t.Fatal("CSV lost precision, null or original text")
	}
	if rows[1][column("snapshot_scope")] != "current_authorized_visible_rows_only" || rows[1][column("original_currency")] != "KWD" || rows[1][column("base_currency")] != "EUR" {
		t.Fatal("snapshot/denominations are ambiguous")
	}
	if err = file.append("reconciliation", financeExportFlatten("reconciliation", Record{"starts_on": "2026-10-01", "opening_minor": "0", "currency": "EUR", "exponent": 2})); err != nil {
		t.Fatal(err)
	}
	all, err := csv.NewReader(bytes.NewReader(file.buffer.Bytes())).ReadAll()
	if err != nil || all[3][column("starts_on")] != "2026-10-01" || all[3][column("opening_minor")] != "0" {
		t.Fatal("explicit statement period baseline was lost or became unknown")
	}
}

func TestFinanceExportAllOrErrorBudgets(t *testing.T) {
	file, err := newFinanceExportCSV(financialScope{generation: 1}, FinanceQuery{Pane: "accounts"})
	if err != nil {
		t.Fatal(err)
	}
	for range financeExportRowsLimit {
		if err = file.append("account_balance", Record{"currency": "EUR", "exponent": 2, "visible_balance_minor": nil}); err != nil {
			t.Fatal("exact row boundary refused", err)
		}
	}
	before := file.buffer.Len()
	if err = file.append("account_balance", Record{}); err == nil || file.buffer.Len() != before {
		t.Fatal("10001st row was partially accepted")
	}
	large, err := newFinanceExportCSV(financialScope{generation: 1}, FinanceQuery{Pane: "accounts"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for range 10 {
		if err = large.append("account_balance", Record{"notes": strings.Repeat("ä", 1<<19)}); err != nil {
			found = true
			break
		}
	}
	if !found || large.buffer.Len() > financeExportBytesLimit {
		t.Fatal("actual UTF8 CSV byte limit not enforced")
	}
}

func TestFinanceExportClosedSelectionAndTwoSlots(t *testing.T) {
	for _, test := range []struct {
		name  string
		query FinanceQuery
	}{
		{"planning", FinanceQuery{Pane: "planning"}}, {"import_configuration", FinanceQuery{Pane: "import"}}, {"partial_cursor", FinanceQuery{Pane: "entries", Cursor: "opaque"}}, {"detail_id", FinanceQuery{Pane: "accounts", ID: "00000000-0000-4000-8000-000000000001"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateFinanceExport(test.query); err == nil {
				t.Fatal("unsupported or partial selection accepted")
			}
		})
	}
	financeExportSlots <- struct{}{}
	financeExportSlots <- struct{}{}
	defer func() { <-financeExportSlots; <-financeExportSlots }()
	store := &Store{}
	raw, err := store.FinanceExport(context.Background(), auth.Identity{}, FinanceQuery{Pane: "entries", Month: "2026-10"})
	if !errors.Is(err, ErrConflict) || raw != nil {
		t.Fatal("third concurrent export reached storage or emitted bytes")
	}
}
