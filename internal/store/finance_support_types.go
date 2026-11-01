package store

import "encoding/json"

type FinanceFilter struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Account  string `json:"account"`
	Person   string `json:"person"`
	Category string `json:"category"`
	Payee    string `json:"payee"`
	State    string `json:"state"`
}

type FinanceFilterInput struct {
	Name     string        `json:"name"`
	Criteria FinanceFilter `json:"criteria"`
}

type FinanceBudgetScope struct {
	Account string `json:"account"`
	Person  string `json:"person"`
}

type FinanceBudgetInput struct {
	Name              string             `json:"name"`
	CategoryID        string             `json:"category_id"`
	StartsOn          string             `json:"starts_on"`
	EndsOn            *string            `json:"ends_on"`
	MonthlyMinor      string             `json:"monthly_minor"`
	CarryRule         string             `json:"carry_rule"`
	InitialCarryMinor string             `json:"initial_carry_minor"`
	Scope             FinanceBudgetScope `json:"scope"`
}

type FinanceGoalInput struct {
	Name               string  `json:"name"`
	Currency           string  `json:"currency"`
	Exponent           int     `json:"exponent"`
	TargetMinor        string  `json:"target_minor"`
	TargetOn           *string `json:"target_on"`
	AccountID          *string `json:"account_id"`
	PlannedPeriodMinor *string `json:"planned_period_minor"`
	PlannedPeriod      *string `json:"planned_period"`
	State              string  `json:"state"`
}

type FinanceContributionInput struct {
	GoalID           string  `json:"goal_id"`
	GoalVersion      int64   `json:"goal_version"`
	OccurredOn       string  `json:"occurred_on"`
	AmountMinor      string  `json:"amount_minor"`
	ContributionKind string  `json:"contribution_kind"`
	EventID          *string `json:"event_id"`
	MovementID       *string `json:"movement_id"`
	Note             string  `json:"note"`
}

type FinanceContributionReverse struct {
	OriginalID      string `json:"original_id"`
	OriginalVersion int64  `json:"original_version"`
	OccurredOn      string `json:"occurred_on"`
	Note            string `json:"note"`
}

type FinanceRecurrenceInput struct {
	TemplateKind   string          `json:"template_kind,omitempty"`
	Name           string          `json:"name"`
	AnchorOn       string          `json:"anchor_on"`
	EndsOn         *string         `json:"ends_on"`
	RepeatUnit     string          `json:"repeat_unit"`
	RepeatInterval int             `json:"repeat_interval"`
	MonthdayPolicy string          `json:"monthday_policy"`
	Template       json.RawMessage `json:"template"`
	AccountID      *string         `json:"account_id"`
	Active         bool            `json:"active"`
	Automatic      bool            `json:"automatic"`
}

type FinanceGenerateInput struct {
	Through string `json:"through"`
}

type FinanceRuleInput struct {
	Name         string  `json:"name"`
	Priority     int     `json:"priority"`
	Active       bool    `json:"active"`
	MatchKind    string  `json:"match_kind"`
	PayeePattern string  `json:"payee_pattern"`
	Currency     *string `json:"currency"`
	MinimumMinor *string `json:"minimum_minor"`
	MaximumMinor *string `json:"maximum_minor"`
	CategoryID   string  `json:"category_id"`
	AccountID    *string `json:"account_id"`
}

type FinanceRuleExplainInput struct {
	Payee       string  `json:"payee"`
	Currency    string  `json:"currency"`
	AmountMinor string  `json:"amount_minor"`
	AccountID   *string `json:"account_id"`
}

type FinanceBankFields struct {
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Debit       string `json:"debit"`
	Credit      string `json:"credit"`
	Currency    string `json:"currency"`
	Payee       string `json:"payee"`
	Description string `json:"description"`
	Reference   string `json:"reference"`
	ExternalID  string `json:"external_id"`
}

type FinanceBankProfileInput struct {
	Name             string            `json:"name"`
	AccountID        string            `json:"account_id"`
	Delimiter        string            `json:"delimiter"`
	Encoding         string            `json:"encoding"`
	DateLayout       string            `json:"date_layout"`
	DecimalSeparator string            `json:"decimal_separator"`
	GroupSeparator   string            `json:"group_separator"`
	AmountMode       string            `json:"amount_mode"`
	FieldMap         FinanceBankFields `json:"field_map"`
}

type FinanceReceiptLine struct {
	ID          string  `json:"id"`
	Position    int     `json:"position"`
	Label       string  `json:"label"`
	Quantity    *string `json:"quantity"`
	UnitPrice   *string `json:"unit_price"`
	AmountMinor *string `json:"amount_minor"`
	SourceText  string  `json:"source_text"`
	SourceKind  string  `json:"source_kind"`
	PageNumber  int     `json:"page_number"`
	SourceLine  int     `json:"source_line"`
	Confidence  *string `json:"confidence"`
	EntryLineID *string `json:"entry_line_id"`
	Reviewed    bool    `json:"reviewed"`
}

type FinanceReceiptInput struct {
	EntryID                *string              `json:"entry_id"`
	EntryVersion           *int64               `json:"entry_version"`
	CaptureJobID           *string              `json:"capture_job_id"`
	AttachmentID           string               `json:"attachment_id"`
	SourceVersion          int64                `json:"source_version"`
	SourceHash             string               `json:"source_hash"`
	Currency               string               `json:"currency"`
	Exponent               int                  `json:"exponent"`
	ReceiptTotalMinor      *string              `json:"receipt_total_minor"`
	State                  string               `json:"state"`
	DifferenceAcknowledged bool                 `json:"difference_acknowledged"`
	Lines                  []FinanceReceiptLine `json:"lines"`
}
