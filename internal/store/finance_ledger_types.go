package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type FinancePaymentInput struct {
	AccountID          string           `json:"account_id"`
	AccountVersion     int64            `json:"account_version"`
	NativeMinor        string           `json:"native_minor"`
	FX                 *finance.FXInput `json:"fx"`
	DifferenceReviewed bool             `json:"difference_reviewed"`
}

type FinanceEntryLineInput struct {
	ID            string `json:"id"`
	Version       int64  `json:"version"`
	CategoryID    string `json:"category_id"`
	OriginalMinor string `json:"original_minor"`
	Note          string `json:"note"`
}

type FinanceEntryInput struct {
	OccurredOn         string                    `json:"occurred_on"`
	Description        string                    `json:"description"`
	Payee              string                    `json:"payee"`
	Kind               string                    `json:"kind"`
	Money              finance.MoneyInput        `json:"money"`
	Payment            *FinancePaymentInput      `json:"payment"`
	PaidByMembershipID *string                   `json:"paid_by_membership_id"`
	Lines              []FinanceEntryLineInput   `json:"lines"`
	Baseline           []expenseedit.LineVersion `json:"baseline"`
	Removed            []expenseedit.LineVersion `json:"removed"`
	Split              *finance.SplitInput       `json:"split"`
}

type FinanceIncomeInput struct {
	OccurredOn  string               `json:"occurred_on"`
	Description string               `json:"description"`
	Payee       string               `json:"payee,omitempty"`
	Money       finance.MoneyInput   `json:"money"`
	Payment     *FinancePaymentInput `json:"payment"`
	State       string               `json:"state,omitempty"`
}

type FinanceSourceVersion struct {
	EntryID string `json:"entry_id"`
	Version int64  `json:"version"`
}

type financeReviewedSourcesKey struct{}

func financeSourceReviewStamp(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, own []FinanceResourceRef) (string, error) {
	_, refs := financeOriginFor(ctx, own)
	if reviewed, ok := ctx.Value(financeReviewedSourcesKey{}).([]FinanceResourceRef); ok {
		refs = append(append([]FinanceResourceRef{}, reviewed...), own...)
	}
	return financeProspectiveAccess(ctx, tx, id, scope, refs, "view")
}

func financeLedgerCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, command FinanceCommand) (Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	switch command.Resource {
	case "accounts":
		return financeAccountCommand(ctx, tx, id, scope, command)
	case "cash":
		return financeCashCommand(ctx, tx, id, scope, command)
	case "entries":
		return financeEntryCommand(ctx, tx, id, scope, command)
	case "reconciliations":
		return financeReconciliationCommand(ctx, tx, id, scope, command)
	default:
		return nil, ErrNotFound
	}
}

func financeLedgerRecord(ctx context.Context, tx pgx.Tx, household, table, rid string, lock bool) (Record, error) {
	allowed := map[string]bool{"accounts": true, "entries": true, "cash_events": true, "reconciliations": true}
	if !allowed[table] || !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE NOWAIT"
	}
	var raw []byte
	projection := "to_jsonb(t)"
	if table == "entries" {
		projection = "CASE WHEN octet_length(t.description)::bigint+octet_length(t.payee)::bigint<=1048576 THEN to_jsonb(t) END"
	}
	err := tx.QueryRow(ctx, "SELECT "+projection+" FROM "+pgx.Identifier{"expenses", table}.Sanitize()+" t WHERE household_id=$1 AND id=$2"+suffix, household, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	if len(raw) == 0 || len(raw) > 1<<20 {
		return nil, Invalid{"Gespeicherte Buchung überschreitet die sichere Bearbeitungsgröße"}
	}
	out, err := decode(raw)
	if err != nil {
		return nil, err
	}
	for key, v := range out {
		if v != nil && (strings.HasSuffix(key, "_minor") || strings.HasSuffix(key, "_numerator") || strings.HasSuffix(key, "_denominator")) {
			out[key] = String(v)
		}
	}
	return out, nil
}

func financeLedgerResult(resource, rid string, version int64, impact Record) Record {
	state := String(impact["state"])
	if state == "" {
		switch resource {
		case "entries":
			if impact["action"] == "save" {
				state = "draft"
			} else {
				state = "posted"
			}
		case "cash":
			state = "posted"
		case "reconciliations":
			state = "confirmed"
			if impact["reopened_id"] != nil {
				state = "reopened"
			}
		}
	}
	return Record{"resource": resource, "id": rid, "version": version, "state": state, "impact": impact, "result_ids": []string{rid}}
}

func financeMinorValue(value *int64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatInt(*value, 10)
}

func financeRateValues(rate *finance.FXInput) (any, any, any, any) {
	if rate == nil {
		return nil, nil, nil, nil
	}
	return rate.Numerator, rate.Denominator, rate.EffectiveOn, rate.Source
}

func financeAccountForPayment(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, payment *FinancePaymentInput, allowUnknown bool) (Record, finance.Money, error) {
	if payment == nil || !value.ValidUUID(payment.AccountID) || payment.AccountVersion <= 0 {
		return nil, finance.Money{}, Invalid{"Aktuelles Zahlungskonto und nativen Betrag prüfen"}
	}
	if _, err := financeResourceAccess(ctx, tx, id, scope, "accounts", payment.AccountID, "edit"); err != nil {
		return nil, finance.Money{}, err
	}
	account, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "accounts", payment.AccountID, true)
	if err != nil {
		return nil, finance.Money{}, err
	}
	if Int(account["version"]) != payment.AccountVersion || account["archived_at"] != nil {
		return nil, finance.Money{}, ErrConflict
	}
	money, err := finance.NormalizeMoney(finance.MoneyInput{Currency: String(account["currency_code"]), Exponent: int(Int(account["currency_exponent"])), Minor: payment.NativeMinor, FX: payment.FX}, false, allowUnknown)
	if err != nil || money.Minor <= 0 {
		return nil, finance.Money{}, Invalid{"Exakten positiven nativen Zahlungsbetrag und Kontokurs prüfen"}
	}
	return account, money, nil
}

func financeRaw(input any) (json.RawMessage, error) {
	return json.Marshal(input)
}
