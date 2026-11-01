//go:build integration

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
	"household/internal/config"
	"household/internal/finance"
	"household/internal/value"
)

func financeExportReadCSV(t *testing.T, raw []byte) ([]string, []map[string]string) {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil || len(rows) == 0 {
		t.Fatal("actual complete CSV is invalid")
	}
	records := make([]map[string]string, 0, len(rows)-1)
	for _, values := range rows[1:] {
		if len(values) != len(rows[0]) {
			t.Fatal("CSV column count changed")
		}
		row := map[string]string{}
		for index, key := range rows[0] {
			row[key] = values[index]
		}
		records = append(records, row)
	}
	return rows[0], records
}

func TestFinanceExportCompletePagesExactUnknownZeroAndNative(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	ids := map[string]bool{}
	for index := range 51 {
		rid := value.UUID()
		input := financeNativeEntry(category, "1")
		input.Payee = "Export page source"
		if index == 0 {
			input.Description = "=SUM(1,2)\nUnchanged original text"
		}
		financeNativeApply(t, f, "entries", "save", rid, 0, input)
		ids[rid] = true
	}
	unknown, zero := value.UUID(), value.UUID()
	foreign := financeNativeEntry(category, "1")
	foreign.Payee = "Export page source"
	foreign.Money.Currency, foreign.Money.Exponent = "KWD", 3
	financeNativeApply(t, f, "entries", "save", unknown, 0, foreign)
	ids[unknown] = true
	foreign.Money.FX = &finance.FXInput{Numerator: "1", Denominator: "1", EffectiveOn: "2026-10-30", Source: "Explicit native export valuation"}
	foreign.Lines[0].ID = value.UUID()
	financeNativeApply(t, f, "entries", "post", zero, 0, foreign)
	ids[zero] = true
	account, version := financeNativeAccount(t, f, "EUR", 2)
	linked := value.UUID()
	paid := financeNativeEntry(category, "100")
	paid.Payee = "Export page source"
	paid.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "100"}
	financeNativeApply(t, f, "entries", "post", linked, 0, paid)
	ids[linked] = true
	income := value.UUID()
	if err := f.owner.QueryRow(f.ctx, "SELECT version FROM expenses.accounts WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, account).Scan(&version); err != nil {
		t.Fatal(err)
	}
	financeNativeApply(t, f, "cash", "income", income, 0, FinanceIncomeInput{OccurredOn: "2026-10-30", Description: "Actual exported income movement", Payee: "Export page source", Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "100"}, Payment: &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "100"}})
	ids[income] = true
	query := FinanceQuery{Pane: "entries", Month: "2026-10", Payee: "Export page source"}
	first, err := f.store.FinanceView(f.ctx, f.identity, query)
	if err != nil || String(first["next_cursor"]) == "" {
		t.Fatal("fixture did not exercise a genuine second page")
	}
	raw, err := f.store.FinanceExport(f.ctx, f.identity, query)
	if err != nil {
		t.Fatal(err)
	}
	_, rows := financeExportReadCSV(t, raw)
	if len(rows) != len(ids) {
		t.Fatal("export silently stopped at a table page")
	}
	seen := map[string]bool{}
	foundUnknown, foundZero, foundNative, foundIncome, foundFormula := false, false, false, false, false
	for _, row := range rows {
		rid := row["id"]
		if !ids[rid] || seen[rid] || row["snapshot_scope"] != "current_authorized_visible_rows_only" || row["original_minor"] == "" {
			t.Fatal("export selection, identity or precision changed")
		}
		seen[rid] = true
		if rid == unknown {
			foundUnknown = row["base_minor"] == "" && row["original_minor"] == "1" && row["original_currency"] == "KWD"
		}
		if rid == zero {
			foundZero = row["base_minor"] == "0" && row["base_status"] == "below_base_precision" && row["original_minor"] == "1"
		}
		if rid == linked {
			foundNative = row["payment_native_minor"] == "-100" && row["payment_base_minor"] == "-100" && row["native_currency"] == "EUR" && row["base_minor"] == "100"
		}
		if rid == income {
			foundIncome = row["row_group"] == "income" && row["payment_native_minor"] == "100" && row["payment_base_minor"] == "100" && row["native_currency"] == "EUR"
		}
		if strings.Contains(row["description"], "Unchanged original text") {
			foundFormula = strings.HasPrefix(row["description"], "'=SUM(1,2)")
		}
	}
	if !foundUnknown || !foundZero || !foundNative || !foundIncome || !foundFormula {
		t.Fatal("CSV conflated null/zero/native direction or unsafe text presentation")
	}
	var original string
	if err = f.owner.QueryRow(f.ctx, "SELECT description FROM expenses.entries WHERE household_id=$1 AND description LIKE '=SUM%Unchanged original text'", f.identity.HouseholdID).Scan(&original); err != nil || original != "=SUM(1,2)\nUnchanged original text" {
		t.Fatal("CSV presentation modified the financial fact")
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if partial, err := f.store.FinanceExport(ctx, f.identity, query); !errors.Is(err, context.Canceled) || partial != nil {
		t.Fatal("cancelled export emitted partial financial bytes")
	}
	if f.app.Stat().AcquiredConns() != 0 {
		t.Fatal("export retained a database connection")
	}
}

func TestFinanceExportCurrentReaderAndSelectedSourceDenial(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	visible := value.UUID()
	financeNativeApply(t, f, "entries", "post", visible, 0, financeNativeEntry(category, "100"))
	private := value.UUID()
	input := financeNativeEntry(category, "200")
	input.Description = "Private export exclusion sentinel"
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	generation, stamp := financeNativeScope(t, f, "entries", private, 0)
	responsible := f.identity.MembershipID
	command := FinanceCommand{Resource: "entries", Action: "post", ID: private, Generation: generation, AccessVersion: stamp, Payload: raw, InitialAccess: &FinanceInitialAccess{AccessMode: "selected", ResponsibleMembershipID: &responsible, Grants: []FinanceInitialGrant{{MembershipID: responsible, Permission: "edit"}}}}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal(err)
	}
	command.ReviewHash = String(preview["impact_hash"])
	if _, err = f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); err != nil {
		t.Fatal(err)
	}
	member := financeNativeMember(t, f)
	var username string
	if err = f.owner.QueryRow(f.ctx, "UPDATE core.users SET must_change_password=false WHERE id=(SELECT user_id FROM core.memberships WHERE id=$1) RETURNING username", member).Scan(&username); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(f.app, config.Config{EncryptionKey: auth.RandomToken(), RateSecret: auth.RandomToken(), CSRFSecret: auth.RandomToken(), SessionIdle: 24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := a.Login(f.ctx, username, recipeExpansionPassword, "", "127.0.0.1", "Finance export Reader")
	if err != nil || reader.Role != "reader" {
		t.Fatal("actual reader authentication failed")
	}
	query := FinanceQuery{Pane: "entries", Month: "2026-10"}
	export, err := f.store.FinanceExport(f.ctx, reader, query)
	if err != nil {
		t.Fatal(err)
	}
	_, rows := financeExportReadCSV(t, export)
	if len(rows) != 1 || rows[0]["id"] != visible || bytes.Contains(export, []byte(private)) || bytes.Contains(export, []byte("Private export exclusion sentinel")) {
		t.Fatal("Reader export disclosed selected-private rows, IDs or labels")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.memberships SET inactive_at=clock_timestamp() WHERE id=$1", member); err != nil {
		t.Fatal(err)
	}
	if partial, err := f.store.FinanceExport(f.ctx, reader, query); !errors.Is(err, ErrForbidden) || partial != nil {
		t.Fatal("retired membership emitted a stale or partial CSV")
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if partial, err := f.store.FinanceExport(f.ctx, foreign, query); !errors.Is(err, ErrForbidden) || partial != nil {
		t.Fatal("foreign current scope exported another household")
	}
}

func TestFinanceExportAllOverviewGroupsAndSupportedPanes(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	for range 51 {
		category := financeNativeCategory(t, f)
		financeNativeApply(t, f, "entries", "post", value.UUID(), 0, financeNativeEntry(category, "1"))
	}
	query := FinanceQuery{Pane: "overview", Month: "2026-10"}
	view, err := f.store.FinanceView(f.ctx, f.identity, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(view["rows"].([]Record)) != 50 {
		t.Fatal("fixture did not reach the bounded overview category table")
	}
	raw, err := f.store.FinanceExport(f.ctx, f.identity, query)
	if err != nil {
		t.Fatal(err)
	}
	_, rows := financeExportReadCSV(t, raw)
	if len(rows) != 51 {
		t.Fatal("overview export silently inherited the top50 display bound")
	}
	for _, row := range rows {
		if row["row_group"] != "category_consumption" || row["amount_minor"] != "1" || row["currency"] != "EUR" {
			t.Fatal("overview grouped facts differ")
		}
	}
	for _, pane := range []string{"accounts", "people", "reports"} {
		t.Run(pane, func(t *testing.T) {
			raw, err := f.store.FinanceExport(f.ctx, f.identity, FinanceQuery{Pane: pane, Month: "2026-10"})
			if err != nil {
				t.Fatal(err)
			}
			_, rows := financeExportReadCSV(t, raw)
			if pane == "reports" {
				groups := map[string]int{}
				for _, row := range rows {
					groups[row["row_group"]]++
				}
				if groups["period_consumption"] != 1 || groups["category_consumption"] != 51 {
					t.Fatal("report export omitted actual monthly/category groups")
				}
			} else if len(rows) != 0 {
				t.Fatal("empty account/person scope invented financial facts")
			}
		})
	}
}
