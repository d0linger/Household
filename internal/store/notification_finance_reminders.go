package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// FinancePaymentReminderInput changes reminder metadata, never ledger facts.
type FinancePaymentReminderInput struct {
	Action              string `json:"action"`
	Version             int64  `json:"version"`
	EntryVersion        int64  `json:"entry_version"`
	Generation          int64  `json:"generation"`
	AccessVersion       string `json:"access_version"`
	DueOn               string `json:"due_on"`
	ResponsibleMemberID string `json:"responsible_member_id"`
	RequestID           string `json:"request_id"`
}

type financePaymentReminder struct {
	Version               int64      `json:"version"`
	DueOn                 string     `json:"due_on"`
	ResponsibleMemberID   string     `json:"responsible_member_id"`
	ResponsibleName       string     `json:"responsible_name"`
	State                 string     `json:"state"`
	ConfirmedEntryVersion *int64     `json:"confirmed_entry_version"`
	ConfirmedAt           *time.Time `json:"confirmed_at"`
	ResponsibleSince      time.Time  `json:"responsible_since"`
	CompletedAt           *time.Time `json:"completed_at"`
}

type financePaymentReminderSource struct {
	EntryID, DueOn, ResponsibleMembershipID, ResponsibleUserID, Timezone string
	Version, EntryVersion, PermissionEpoch, ModuleGeneration             int64
	ConfirmedAt                                                          *time.Time
	ResponsibleSince, SourceUpdated, ModuleActivation                    time.Time
	PermissionChangedAt                                                  *time.Time
	Eligible, VerifiedPaid, NeedsReconfirmation, CanEdit, Reversed       bool
	AccessVersion                                                        string
	Reminder                                                             *financePaymentReminder
}

// readFinancePaymentReminderSource requires a real current identity. Notification
// workers supply the registered device's session; no household-wide totals or
// sessionless financial authority are inferred from a reminder assignment.
func readFinancePaymentReminderSource(ctx context.Context, tx pgx.Tx, id auth.Identity, entryID string) (financePaymentReminderSource, error) {
	var out financePaymentReminderSource
	if !value.ValidUUID(entryID) || id.SessionID == "" {
		return out, ErrForbidden
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	var visible bool
	err = tx.QueryRow(ctx, `SELECT core.finance_resource_visible($1,$2,$3,$4,$5,$6,'entries',$7,'view')`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, entryID).Scan(&visible)
	if err != nil {
		return out, err
	}
	if !visible {
		return out, ErrNotFound
	}
	stamp, err := financeReminderAccessStamp(ctx, tx, id, scope, entryID, "view")
	if err != nil {
		return out, err
	}
	out.EntryID, out.AccessVersion, out.Timezone, out.ModuleGeneration = entryID, stamp, scope.timezone, scope.generation
	var kind, state string
	err = tx.QueryRow(ctx, `SELECT e.version,e.updated_at,e.kind,e.state,ms.changed_at,
 coalesce(p.epoch,0),p.updated_at,
 core.finance_resource_visible($1,$3,$4,$5,$6,$7,'entries',e.id,'edit')
 FROM expenses.entries e JOIN core.module_states ms ON ms.household_id=e.household_id AND ms.module_id='expenses'
 LEFT JOIN core.notification_financial_epochs p ON p.household_id=e.household_id
 WHERE e.household_id=$1 AND e.id=$2`, id.HouseholdID, entryID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation).
		Scan(&out.EntryVersion, &out.SourceUpdated, &kind, &state, &out.ModuleActivation, &out.PermissionEpoch, &out.PermissionChangedAt, &out.CanEdit)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.CanEdit = out.CanEdit && id.CanWrite() && kind == "expense"
	if out.CanEdit {
		out.AccessVersion, err = financeReminderAccessStamp(ctx, tx, id, scope, entryID, "edit")
		if err != nil {
			return out, err
		}
	}
	out.VerifiedPaid, err = financePaymentVerified(ctx, tx, id, scope, entryID)
	if err != nil {
		return out, err
	}
	var reversed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entry_relations r
 JOIN expenses.entries e ON e.household_id=r.household_id AND e.id=r.entry_id
 WHERE r.household_id=$1 AND r.original_entry_id=$2 AND r.relation IN('reversal_of','correction_of') AND e.state='posted')`, id.HouseholdID, entryID).Scan(&reversed)
	if err != nil {
		return out, err
	}
	out.Reversed = reversed
	var reminder financePaymentReminder
	var responsibleActive bool
	err = tx.QueryRow(ctx, `SELECT r.version,r.due_on::text,r.responsible_member_id::text,u.display_name,r.state,
 r.confirmed_entry_version,r.confirmed_at,r.responsible_since,r.completed_at,u.id::text,
 m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 FROM expenses.notification_payment_reminders r
 JOIN core.memberships m ON m.household_id=r.household_id AND m.id=r.responsible_member_id
 JOIN core.users u ON u.id=m.user_id WHERE r.household_id=$1 AND r.id=$2`, id.HouseholdID, entryID).
		Scan(&reminder.Version, &reminder.DueOn, &reminder.ResponsibleMemberID, &reminder.ResponsibleName, &reminder.State,
			&reminder.ConfirmedEntryVersion, &reminder.ConfirmedAt, &reminder.ResponsibleSince, &reminder.CompletedAt, &out.ResponsibleUserID, &responsibleActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Reminder, out.Version, out.DueOn = &reminder, reminder.Version, reminder.DueOn
	out.ResponsibleMembershipID, out.ResponsibleSince, out.ConfirmedAt = reminder.ResponsibleMemberID, reminder.ResponsibleSince, reminder.ConfirmedAt
	out.NeedsReconfirmation = reminder.State == "confirmed" && (reminder.ConfirmedEntryVersion == nil || *reminder.ConfirmedEntryVersion != out.EntryVersion)
	out.Eligible = kind == "expense" && (state == "draft" || state == "posted") && !reversed && !out.VerifiedPaid &&
		reminder.State == "confirmed" && reminder.ConfirmedAt != nil && !out.NeedsReconfirmation && responsibleActive &&
		out.ResponsibleUserID == id.UserID && out.ResponsibleMembershipID == id.MembershipID
	return out, nil
}

// Preserve operational SQL errors after the boolean permission check. In
// particular, a concurrent revocation may abort this transaction; workers must
// retry it rather than treating the resulting SQL exception as a clean denial.
func financeReminderAccessStamp(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, entryID, intent string) (string, error) {
	refs, err := json.Marshal([]FinanceResourceRef{{Kind: "entries", ID: entryID}})
	if err != nil {
		return "", err
	}
	var stamp string
	err = tx.QueryRow(ctx, `SELECT core.finance_authority($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, refs, intent).Scan(&stamp)
	return stamp, err
}

// A posted cash event without a real matching account movement is not evidence
// of payment. The event, account and movement must also remain visible to the
// actual caller. Values are compared in SQL and never copied into reminders.
func financePaymentVerified(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, entryID string) (bool, error) {
	var paid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entries e
 JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id
 JOIN expenses.cash_events c ON c.household_id=e.household_id AND c.entry_id=e.id
 JOIN expenses.account_movements m ON m.household_id=c.household_id AND m.event_id=c.id AND m.account_id=f.account_id
 WHERE e.household_id=$1 AND e.id=$2 AND e.state='posted' AND e.kind='expense' AND c.state='posted' AND c.event_kind='expense'
 AND f.account_id IS NOT NULL AND c.account_id=f.account_id AND c.payment_context IS NOT NULL
 AND m.currency_code=f.payment_currency AND m.currency_exponent=f.payment_exponent
 AND m.amount_minor=f.payment_minor AND m.base_minor=f.payment_base_minor
 AND core.finance_resource_visible($1,$3,$4,$5,$6,$7,'cash_events',c.id,'view')
 AND core.finance_resource_visible($1,$3,$4,$5,$6,$7,'account_movements',m.id,'view')
 AND core.finance_resource_visible($1,$3,$4,$5,$6,$7,'accounts',f.account_id,'view'))`,
		id.HouseholdID, entryID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation).Scan(&paid)
	return paid, err
}

func financeReminderRecipientAllowed(ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64, entryID, member string) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT core.finance_notification_recipient_allowed($1,$2,$3,$4,$5,$6,$7,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, generation, entryID, member).Scan(&allowed)
	return allowed, financeAuthorityError(err)
}

func financePaymentReminderProjection(ctx context.Context, tx pgx.Tx, id auth.Identity, source financePaymentReminderSource) (Record, error) {
	members := []Record{}
	more := false
	if source.CanEdit {
		rows, err := tx.Query(ctx, `SELECT m.id::text,u.display_name
 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND core.finance_notification_recipient_allowed($1,$2,$3,$4,$5,$6,$7,m.id)
 ORDER BY u.display_name,m.id LIMIT 51`, id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, source.ModuleGeneration, source.EntryID)
		if err != nil {
			return nil, financeAuthorityError(err)
		}
		for rows.Next() {
			var member, label string
			if err = rows.Scan(&member, &label); err != nil {
				rows.Close()
				return nil, err
			}
			members = append(members, Record{"id": member, "label": label})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		more = len(members) > 50
		if more {
			members = members[:50]
		}
	}
	canConfirm := source.CanEdit && !source.VerifiedPaid && !source.Reversed && source.Reminder != nil &&
		(source.Reminder.State == "draft" || source.NeedsReconfirmation)
	return Record{"entry_id": source.EntryID, "entry_version": source.EntryVersion, "generation": source.ModuleGeneration,
		"access_version": source.AccessVersion, "verified_paid": source.VerifiedPaid, "needs_reconfirmation": source.NeedsReconfirmation,
		"can_edit": source.CanEdit, "can_confirm": canConfirm, "members": members, "members_more": more, "reminder": source.Reminder}, nil
}

// FinancePaymentReminder returns only metadata inherited from a visible entry.
func (s *Store) FinancePaymentReminder(ctx context.Context, id auth.Identity, entryID string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	source, err := readFinancePaymentReminderSource(ctx, tx, id, entryID)
	if err != nil {
		return nil, err
	}
	out, err := financePaymentReminderProjection(ctx, tx, id, source)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
