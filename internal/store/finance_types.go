package store

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/finance"
	"household/internal/value"
)

// FinanceQuery is a closed selection shared by workspace, reports and export.
type FinanceQuery struct {
	Pane, Section, ID, Goal, Month, From, To, Account, Person, Category, Payee, State, Cursor string
}

// FinanceCommand keeps caller input distinct from server-derived authority and
// money. Every resource decodes Payload into its own closed command type.
type FinanceCommand struct {
	ReviewHash    string                `json:"-"`
	Resource      string                `json:"resource"`
	Action        string                `json:"action"`
	ID            string                `json:"id"`
	Generation    int64                 `json:"generation"`
	Version       int64                 `json:"version"`
	AccessVersion string                `json:"access_version"`
	Payload       json.RawMessage       `json:"payload"`
	ReviewToken   string                `json:"review_token,omitempty"`
	InitialAccess *FinanceInitialAccess `json:"initial_access,omitempty"`
}

type FinanceInitialGrant struct {
	MembershipID string `json:"membership_id"`
	Permission   string `json:"permission"`
}

type FinanceInitialAccess struct {
	AccessMode              string                `json:"access_mode"`
	ResponsibleMembershipID *string               `json:"responsible_membership_id"`
	Grants                  []FinanceInitialGrant `json:"grants"`
}

var financePanes = []string{"overview", "entries", "accounts", "planning", "people", "import", "reports"}

func validateFinanceCommand(c FinanceCommand) error {
	if !value.ValidUUID(c.ID) || c.Generation <= 0 || c.Version < 0 || len(c.AccessVersion) != 64 ||
		len(c.ReviewToken) > 4096 || len(c.Payload) < 2 || len(c.Payload) > financeBodyLimit {
		return Invalid{"Vorgang, Generation, Version und aktuellen Zugriffsstand prüfen"}
	}
	actions := map[string][]string{
		"accounts": {"save", "archive"}, "cash": {"income", "opening", "transfer", "settle", "adjustment"},
		"entries": {"save", "post", "refund", "reverse", "correct"},
		"filters": {"save", "delete"}, "budgets": {"save", "archive"}, "goals": {"save", "archive"},
		"contributions": {"save", "reverse"}, "recurrences": {"save", "pause", "generate"},
		"rules": {"save", "archive", "explain"}, "bank_profiles": {"save", "archive"},
		"reconciliations": {"confirm", "reopen"}, "receipts": {"review"}, "access": {"change", "assign", "recover"},
	}
	if !slices.Contains(actions[c.Resource], c.Action) {
		return Invalid{"Unbekannter Finanzvorgang"}
	}
	if c.InitialAccess != nil {
		if c.Version != 0 || !slices.Contains([]string{"accounts", "cash", "entries", "budgets", "goals", "recurrences", "rules", "reconciliations", "receipts"}, c.Resource) ||
			!slices.Contains([]string{"save", "post", "income", "opening", "adjustment", "transfer", "settle", "refund", "reverse", "correct", "review", "confirm"}, c.Action) ||
			len(c.InitialAccess.Grants) > 100 || c.InitialAccess.Grants == nil ||
			!slices.Contains([]string{"household", "selected"}, c.InitialAccess.AccessMode) {
			return Invalid{"Anfangsfreigabe gehört ausschließlich zu einer neuen Finanzressource"}
		}
	}
	return nil
}

func validateFinanceQuery(q FinanceQuery) error {
	if !slices.Contains(financePanes, q.Pane) || len(q.Section) > 32 || len(q.Cursor) > 512 ||
		len(q.Payee) > 400 || !utf8.ValidString(q.Payee) || utf8.RuneCountInString(q.Payee) > 100 {
		return Invalid{"Finanzauswahl erneut prüfen"}
	}
	for _, id := range []string{q.ID, q.Goal, q.Account, q.Person, q.Category} {
		if id != "" && !value.ValidUUID(id) {
			return Invalid{"Gültige Finanzreferenz auswählen"}
		}
	}
	if q.Month != "" {
		month, err := time.Parse("2006-01", q.Month)
		if err != nil || month.Format("2006-01") != q.Month || month.Year() < 1900 || month.Year() > 2200 {
			return Invalid{"Gültigen Monat auswählen"}
		}
	}
	if q.From != "" && !finance.CivilDate(q.From) || q.To != "" && !finance.CivilDate(q.To) ||
		q.From != "" && q.To != "" && q.From > q.To ||
		q.State != "" && !slices.Contains([]string{"draft", "posted", "refunded", "reversed", "corrected"}, q.State) {
		return Invalid{"Zeitraum und Buchungszustand prüfen"}
	}
	sections := map[string][]string{
		"overview": {""}, "entries": {"", "ledger"}, "accounts": {"", "reconciliations"},
		"planning": {"", "budgets", "goals", "contributions", "recurrences", "rules", "cashflow", "filters"},
		"people":   {""}, "import": {"", "bank_profiles", "receipts"}, "reports": {"", "ledger"},
	}
	if !slices.Contains(sections[q.Pane], q.Section) {
		return Invalid{"Unbekannter Finanzbereich"}
	}
	if q.Goal != "" && (q.Pane != "planning" || q.Section != "contributions" || q.ID != "") ||
		q.Pane == "planning" && q.Section == "contributions" && q.Goal == "" && q.ID == "" ||
		q.ID != "" && q.Cursor != "" && (q.Pane != "accounts" || q.Section != "") {
		return Invalid{"Beitragsliste, Zieldetail und Seitenauswahl getrennt öffnen"}
	}
	return nil
}

func financeText(text string, minimum, maximum int) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) == text && len(text) <= maximum*4 &&
		utf8.RuneCountInString(text) >= minimum && utf8.RuneCountInString(text) <= maximum &&
		!strings.ContainsRune(text, '\x00')
}
