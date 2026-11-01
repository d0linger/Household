package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

const financeExportRowsLimit = 10000
const financeExportBytesLimit = 8 << 20

var financeExportSlots = make(chan struct{}, 2)
var financeExportInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,59})$`)

var financeExportColumns = []string{
	"snapshot_scope", "snapshot_at", "expenses_generation", "pane", "section", "row_group",
	"filter_month", "filter_from", "filter_to", "filter_account_id", "filter_person_id", "filter_category_id", "filter_payee", "filter_state",
	"id", "version", "occurred_on", "period", "label", "description", "payee", "notes", "state", "kind",
	"account_id", "person_id", "payer_id", "debtor_id", "creditor_id", "category_id", "as_of",
	"original_currency", "original_exponent", "original_minor", "original_status",
	"base_currency", "base_exponent", "base_minor", "base_status",
	"native_currency", "native_exponent", "payment_native_minor", "payment_base_minor",
	"currency", "exponent", "amount_minor", "visible_balance_minor", "visible_movement_minor", "opening_known",
	"external_minor", "computed_minor", "difference_minor", "starts_on", "opening_minor",
	"fx_numerator", "fx_denominator", "fx_on", "fx_source",
	"payment_fx_numerator", "payment_fx_denominator", "payment_fx_on", "payment_fx_source",
	"source_status", "source_origin", "source_available", "independently_shared", "scope_note",
}

type financeExportBuffer struct{ data bytes.Buffer }

func (b *financeExportBuffer) Len() int      { return b.data.Len() }
func (b *financeExportBuffer) Bytes() []byte { return b.data.Bytes() }

func (b *financeExportBuffer) Write(data []byte) (int, error) {
	if len(data) > financeExportBytesLimit-b.Len() {
		return 0, Invalid{"CSV überschreitet8MiB; Auswahl eingrenzen. Es wird keine Teil-Datei ausgegeben"}
	}
	return b.data.Write(data)
}

type financeExportCSV struct {
	buffer financeExportBuffer
	writer *csv.Writer
	rows   int
	base   map[string]string
}

func newFinanceExportCSV(scope financialScope, query FinanceQuery) (*financeExportCSV, error) {
	payee, err := financeExportText(query.Payee)
	if err != nil {
		return nil, err
	}
	file := &financeExportCSV{base: map[string]string{
		"snapshot_scope": "current_authorized_visible_rows_only",
		"snapshot_at":    scope.readAt.UTC().Format(time.RFC3339Nano), "expenses_generation": strconv.FormatInt(scope.generation, 10),
		"pane": query.Pane, "section": query.Section,
		"filter_month": query.Month, "filter_from": query.From, "filter_to": query.To, "filter_account_id": query.Account, "filter_person_id": query.Person, "filter_category_id": query.Category, "filter_payee": payee, "filter_state": query.State,
	}}
	file.writer = csv.NewWriter(&file.buffer)
	if err := file.writer.Write(financeExportColumns); err != nil {
		return nil, err
	}
	return file, nil
}

func financeExportText(raw string) (string, error) {
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) || len(raw) > 1<<20 {
		return "", Invalid{"Finanztext überschreitet die vollständige Exportgrenze"}
	}
	trimmed := strings.TrimLeftFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) || strings.HasPrefix(raw, "\t") || strings.HasPrefix(raw, "\r") {
		return "'" + raw, nil
	}
	return raw, nil
}

func financeExportMinor(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	raw, ok := value.(string)
	if !ok || !financeExportInteger.MatchString(raw) || raw == "-0" {
		return "", Invalid{"Finanzbetrag ist kein kanonischer exakter Geldstring"}
	}
	return raw, nil
}

func financeExportMap(value any) Record {
	if result, ok := value.(Record); ok {
		return result
	}
	if result, ok := value.(map[string]any); ok {
		return Record(result)
	}
	return nil
}

func financeExportDenomination(code, exponent any) (string, string, error) {
	if code == nil && exponent == nil {
		return "", "", nil
	}
	currency, ok := code.(string)
	if !ok || len(currency) != 3 || strings.Trim(currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return "", "", Invalid{"Exportwährung ist nicht bestätigt"}
	}
	exp := String(exponent)
	want, supported := finance.CurrencyExponent(currency)
	if !supported || exp != strconv.Itoa(want) {
		return "", "", Invalid{"Exportwährungseinheit ist nicht bestätigt"}
	}
	return currency, exp, nil
}

func (file *financeExportCSV) append(group string, data Record) error {
	if file.rows >= financeExportRowsLimit {
		return Invalid{"CSV überschreitet10000Zeilen; Auswahl eingrenzen. Es wird keine Teil-Datei ausgegeben"}
	}
	row := make(map[string]string, len(financeExportColumns))
	for key, value := range file.base {
		row[key] = value
	}
	row["row_group"] = group
	for _, key := range []string{"id", "occurred_on", "period", "state", "kind", "account_id", "person_id", "payer_id", "debtor_id", "creditor_id", "category_id", "as_of", "original_status", "base_status", "source_status", "source_origin", "fx_on", "payment_fx_on", "starts_on"} {
		row[key] = String(data[key])
	}
	for _, key := range []string{"label", "description", "payee", "notes", "scope_note", "fx_source", "payment_fx_source"} {
		value, err := financeExportText(String(data[key]))
		if err != nil {
			return err
		}
		row[key] = value
	}
	if data["version"] != nil {
		value := String(data["version"])
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return Invalid{"Exportversion ist ungültig"}
		}
		row["version"] = value
	}
	for _, prefix := range []string{"original_", "native_", ""} {
		code, exp, err := financeExportDenomination(data[prefix+"currency"], data[prefix+"exponent"])
		if err != nil {
			return err
		}
		row[prefix+"currency"], row[prefix+"exponent"] = code, exp
	}
	row["base_currency"], row["base_exponent"] = "EUR", "2"
	for _, key := range []string{"original_minor", "base_minor", "payment_native_minor", "payment_base_minor", "amount_minor", "visible_balance_minor", "visible_movement_minor", "external_minor", "computed_minor", "difference_minor", "opening_minor"} {
		value, err := financeExportMinor(data[key])
		if err != nil {
			return err
		}
		row[key] = value
	}
	for _, key := range []string{"opening_known", "source_available", "independently_shared"} {
		if data[key] != nil {
			value, ok := data[key].(bool)
			if !ok {
				return Invalid{"Exportstatus ist ungültig"}
			}
			row[key] = strconv.FormatBool(value)
		}
	}
	for _, key := range []string{"fx_numerator", "fx_denominator", "payment_fx_numerator", "payment_fx_denominator"} {
		if data[key] != nil {
			value, err := financeExportMinor(data[key])
			if err != nil || strings.HasPrefix(value, "-") || value == "0" || len(value) > 18 {
				return Invalid{"Exportkurs ist ungültig"}
			}
			row[key] = value
		}
	}
	values := make([]string, len(financeExportColumns))
	for index, key := range financeExportColumns {
		values[index] = row[key]
	}
	if err := file.writer.Write(values); err != nil {
		return err
	}
	file.writer.Flush()
	if err := file.writer.Error(); err != nil {
		return err
	}
	file.rows++
	return nil
}

func financeExportFlatten(group string, record Record) Record {
	row := Record{}
	for key, value := range record {
		row[key] = value
	}
	row["period"] = record["month"]
	if record["membership_id"] != nil {
		row["person_id"] = record["membership_id"]
	}
	row["payer_id"] = record["paid_by_membership_id"]
	if record["debtor"] != nil {
		row["debtor_id"] = record["debtor"]
	}
	if record["creditor"] != nil {
		row["creditor_id"] = record["creditor"]
	}
	if record["balance_minor"] != nil {
		row["visible_balance_minor"] = record["balance_minor"]
	}
	if row["label"] == nil {
		row["label"] = record["name"]
	}
	if record["event_kind"] != nil {
		row["kind"] = record["event_kind"]
	}
	if record["cutoff"] != nil {
		row["occurred_on"] = record["cutoff"]
	}
	if group == "account_balance" {
		row["kind"] = record["account_type"]
		row["scope_note"] = "Saldo sichtbarer Buchungen; ohne bestätigten Anfangsstand bleibt der Saldo leer, kein Banksaldo"
	}
	if group == "person_balance" {
		row["scope_note"] = "Saldo ausschließlich aktuell sichtbarer geprüfter Personenaufteilungen und Ausgleiche; je Originalwährung"
	}
	if group == "category_consumption" || group == "period_consumption" {
		row["scope_note"] = "Gebuchter Nettoverbrauch der vollständigen aktuell berechtigten Filterauswahl"
	}
	if group == "entry" {
		row["scope_note"] = "Vollständiger Buchungskopf passender sichtbarer Einträge; ein Kategorienfilter begrenzt Auswertungssummen, nicht den gespeicherten Gesamtbetrag. Personenfilter bedeutet Beteiligung, keine erfundene Kategorie-/Personenmatrix"
	}
	if money := financeExportMap(record["money"]); money != nil {
		row["original_currency"], row["original_exponent"], row["original_minor"], row["original_status"] = money["currency"], money["exponent"], money["minor"], money["original_status"]
		row["base_minor"], row["base_status"] = money["base_minor"], money["base_status"]
		if fx := financeExportMap(money["fx"]); fx != nil {
			row["fx_numerator"], row["fx_denominator"], row["fx_on"], row["fx_source"] = fx["numerator"], fx["denominator"], fx["effective_on"], fx["source"]
		}
	}
	if origin := financeExportMap(record["source_status"]); origin != nil {
		row["source_status"], row["source_origin"], row["source_available"], row["independently_shared"] = origin["status"], origin["origin"], origin["source_available"], origin["independently_shared"]
	}
	return row
}

func financeExportRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func validateFinanceExport(query FinanceQuery) error {
	if err := validateFinanceQuery(query); err != nil {
		return err
	}
	if !slices.Contains([]string{"overview", "entries", "accounts", "people", "reports"}, query.Pane) || query.Cursor != "" || query.ID != "" || query.Goal != "" {
		return Invalid{"Finanzexport verwendet die ganze Filterauswahl ohne Einzel-ID oder Seitenzeiger; Planung und Importkonfiguration werden nicht exportiert"}
	}
	if query.Pane == "accounts" && query.Section != "" && query.Section != "reconciliations" ||
		(query.Pane == "reports" || query.Pane == "entries") && query.Section != "" && query.Section != "ledger" ||
		query.Pane != "accounts" && query.Pane != "reports" && query.Pane != "entries" && query.Section != "" {
		return Invalid{"Diesen Finanzbereich nicht als Faktenexport verwenden"}
	}
	return nil
}

// FinanceExport materializes the entire authorized selection before exposing a
// byte. The snapshot scope is visible financial facts, never a hidden full ledger.
func (s *Store) FinanceExport(ctx context.Context, id auth.Identity, query FinanceQuery) ([]byte, error) {
	if err := validateFinanceExport(query); err != nil {
		return nil, err
	}
	select {
	case financeExportSlots <- struct{}{}:
		defer func() { <-financeExportSlots }()
	default:
		return nil, ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer financeExportRollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation(scope.timezone)
	if err != nil {
		return nil, err
	}
	if query.Month == "" {
		query.Month = scope.readAt.In(zone).Format("2006-01")
	}
	if _, err = financeFilterReferences(ctx, tx, id, scope, FinanceFilter{From: query.From, To: query.To, Account: query.Account, Person: query.Person, Category: query.Category, Payee: query.Payee, State: query.State}); err != nil {
		return nil, err
	}
	file, err := newFinanceExportCSV(scope, query)
	if err != nil {
		return nil, err
	}
	if query.Pane == "overview" || query.Pane == "reports" && query.Section == "" {
		if query.Pane == "reports" {
			view, readErr := financeReports(ctx, tx, id, scope, query)
			if readErr != nil {
				return nil, readErr
			}
			rows, ok := view["rows"].([]Record)
			if !ok {
				return nil, errors.New("invalid finance report projection")
			}
			for _, row := range rows {
				if err = file.append("period_consumption", financeExportFlatten("period_consumption", row)); err != nil {
					return nil, err
				}
			}
		}
		if err = financeExportCategories(ctx, tx, id, scope, query, file); err != nil {
			return nil, err
		}
	} else {
		if err = financeExportLedger(ctx, tx, id, scope, query, file); err != nil {
			return nil, err
		}
	}
	file.writer.Flush()
	if err = file.writer.Error(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bytes.Clone(file.buffer.Bytes()), nil
}

func financeExportCategories(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, query FinanceQuery, file *financeExportCSV) error {
	where, args, err := financeExpenseSelection(scope, query)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT l.category_id,c.name,sum(l.amount_minor::numeric)::text FROM expenses.entries t JOIN expenses.entry_lines l ON l.household_id=t.household_id AND l.entry_id=t.id JOIN expenses.categories c ON c.household_id=l.household_id AND c.id=l.category_id WHERE t.state='posted' AND ($6='' OR l.category_id=NULLIF($6,'')::uuid) AND `+where+` GROUP BY l.category_id,c.name ORDER BY abs(sum(l.amount_minor::numeric)) DESC,l.category_id LIMIT 10001`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > financeExportRowsLimit {
			return Invalid{"Kategorieexport überschreitet10000Gruppen; keine Teil-Datei"}
		}
		var category, label, minor string
		if err = rows.Scan(&category, &label, &minor); err != nil {
			return err
		}
		if err = file.append("category_consumption", financeExportFlatten("category_consumption", Record{"category_id": category, "label": label, "amount_minor": minor, "currency": "EUR", "exponent": 2})); err != nil {
			return err
		}
	}
	return rows.Err()
}

func financeExportLedger(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, query FinanceQuery, file *financeExportCSV) error {
	seen := map[string]bool{}
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		view, err := financeLedgerRead(ctx, tx, id, scope, query)
		if err != nil {
			return err
		}
		rows, ok := view["rows"].([]Record)
		if !ok {
			return errors.New("invalid finance row projection")
		}
		if query.Pane != "people" || first {
			for _, record := range rows {
				group := "entry"
				if query.Pane == "accounts" {
					group = "account_balance"
					if query.Section == "reconciliations" {
						group = "reconciliation"
					}
				} else if query.Pane == "people" {
					group = "person_balance"
				} else if record["resource"] == "cash" {
					group = "income"
				}
				row := financeExportFlatten(group, record)
				if group == "entry" || group == "income" {
					if err = financeExportPayment(ctx, tx, id, scope, record, row); err != nil {
						return err
					}
				}
				if err = file.append(group, row); err != nil {
					return err
				}
			}
		}
		next := String(view["next_cursor"])
		if query.Pane == "people" {
			detail := financeExportMap(view["detail"])
			history, ok := detail["settlements"].([]Record)
			if !ok {
				return errors.New("invalid finance settlement projection")
			}
			for _, record := range history {
				if err = file.append("settlement", financeExportFlatten("settlement", record)); err != nil {
					return err
				}
			}
			next = String(detail["next_cursor"])
		}
		if next == "" {
			return nil
		}
		if seen[next] || len(seen) >= 201 {
			return ErrConflict
		}
		seen[next] = true
		query.Cursor = next
		first = false
	}
}

func financeExportPayment(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, record, row Record) error {
	rid := String(record["id"])
	if record["resource"] == "cash" {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT payment_context FROM expenses.cash_events WHERE household_id=$1 AND id=$2 AND event_kind='income'`, id.HouseholdID, rid).Scan(&raw); err != nil {
			return err
		}
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		payment, err := DecodeRecord(raw)
		if err != nil {
			return err
		}
		account := String(payment["account_id"])
		if account == "" {
			return nil
		}
		if _, err = financeResourceAccess(ctx, tx, id, scope, "accounts", account, "view"); err != nil {
			return err
		}
		var code string
		var exponent int
		if err = tx.QueryRow(ctx, "SELECT currency_code,currency_exponent FROM expenses.accounts WHERE household_id=$1 AND id=$2", id.HouseholdID, account).Scan(&code, &exponent); err != nil {
			return err
		}
		row["account_id"], row["native_currency"], row["native_exponent"], row["payment_native_minor"] = account, code, exponent, payment["native_minor"]
		if fx := financeExportMap(payment["fx"]); fx != nil {
			row["payment_fx_numerator"], row["payment_fx_denominator"], row["payment_fx_on"], row["payment_fx_source"] = fx["numerator"], fx["denominator"], fx["effective_on"], fx["source"]
		}
		if record["state"] == "posted" {
			predicate, args, err := financeResourcePredicate(scope, "account_movements", "view", "m", 4)
			if err != nil {
				return err
			}
			movements, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('native_minor',m.amount_minor::text,'base_minor',m.base_minor::text,'currency',m.currency_code,'exponent',m.currency_exponent) FROM expenses.account_movements m WHERE m.household_id=$1 AND m.event_id=$2 AND m.account_id=$3 AND `+predicate+` LIMIT 2`, append([]any{id.HouseholdID, rid, account}, args...), 2)
			if err != nil {
				return err
			}
			if len(movements) != 1 {
				return ErrNotFound
			}
			movement := movements[0]
			row["payment_native_minor"], row["payment_base_minor"], row["native_currency"], row["native_exponent"] = movement["native_minor"], movement["base_minor"], movement["currency"], movement["exponent"]
		} else {
			var typed FinancePaymentInput
			if err = decodeFinancePayload(raw, &typed); err != nil {
				return err
			}
			valued, err := finance.NormalizeMoney(finance.MoneyInput{Currency: code, Exponent: exponent, Minor: typed.NativeMinor, FX: typed.FX}, false, true)
			if err != nil {
				return err
			}
			if valued.BaseMinor != nil {
				row["payment_base_minor"] = strconv.FormatInt(*valued.BaseMinor, 10)
			}
		}
		return nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2)", id.HouseholdID, rid).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := financeResourceAccess(ctx, tx, id, scope, "entry_finance", rid, "view"); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT jsonb_build_object('account_id',account_id,'native_currency',payment_currency,'native_exponent',payment_exponent,'payment_native_minor',payment_minor::text,'payment_base_minor',payment_base_minor::text,'payment_fx_numerator',payment_fx_numerator::text,'payment_fx_denominator',payment_fx_denominator::text,'payment_fx_on',payment_fx_on::text,'payment_fx_source',payment_fx_source) FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, rid).Scan(&raw); err != nil {
		return err
	}
	payment, err := DecodeRecord(raw)
	if err != nil {
		return err
	}
	for key, value := range payment {
		row[key] = value
	}
	return nil
}
