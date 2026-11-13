package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/finance"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func validateFinancePaymentReminder(in FinancePaymentReminderInput) error {
	stamp, err := hex.DecodeString(in.AccessVersion)
	if err != nil || len(stamp) != 32 || in.AccessVersion != strings.ToLower(in.AccessVersion) ||
		!slices.Contains([]string{"save", "confirm", "complete", "cancel"}, in.Action) ||
		in.Version < 0 || in.Version >= notification.MaxRevision || in.EntryVersion <= 0 || in.Generation <= 0 ||
		!finance.CivilDate(in.DueOn) || !value.ValidUUID(in.ResponsibleMemberID) || !value.ValidUUID(in.RequestID) {
		return Invalid{"Zahlungserinnerung, Fälligkeit, Zuständigkeit und aktuellen Stand prüfen"}
	}
	return nil
}

// SaveFinancePaymentReminder writes metadata only. Durable receipts contain no
// financial content and replay always rechecks current entry authority before
// returning a fresh projection rather than replaying a stale sensitive body.
func (s *Store) SaveFinancePaymentReminder(ctx context.Context, id auth.Identity, entryID string, in FinancePaymentReminderInput) (Record, error) {
	if err := validateFinancePaymentReminder(in); err != nil {
		return nil, err
	}
	if !value.ValidUUID(entryID) || id.SessionID == "" || !id.CanWrite() {
		return nil, ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), in.RequestID)
	tx, err := s.BeginMutation(ctx, id, "expenses", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	// Reject guessed private entry IDs before acquiring a row lock. The full
	// source is checked again under the lock below, including current versions.
	var editable bool
	err = tx.QueryRow(ctx, `SELECT core.finance_resource_visible($1,$2,$3,$4,$5,$6,'entries',$7,'edit')`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, in.Generation, entryID).Scan(&editable)
	if err != nil {
		return nil, err
	}
	if !editable {
		return nil, ErrNotFound
	}
	const operation = "finance-payment-reminder-v1"
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, operation+"/"+id.HouseholdID+"/"+id.UserID+"/"+in.RequestID); err != nil {
		return nil, err
	}
	// Serialize first insertion and later changes against the existing entry.
	var lockedVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM expenses.entries WHERE household_id=$1 AND id=$2 FOR UPDATE`, id.HouseholdID, entryID).Scan(&lockedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	source, err := readFinancePaymentReminderSource(ctx, tx, id, entryID)
	if err != nil {
		return nil, err
	}
	if !source.CanEdit {
		return nil, ErrForbidden
	}
	raw, err := json.Marshal(struct {
		Entry string
		Input FinancePaymentReminderInput
	}{entryID, in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	var priorHash []byte
	var priorGeneration int64
	err = tx.QueryRow(ctx, `SELECT request_hash,module_generation FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, id.HouseholdID, id.UserID, operation, in.RequestID).Scan(&priorHash, &priorGeneration)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) || priorGeneration != source.ModuleGeneration {
			return nil, ErrConflict
		}
		out, readErr := financePaymentReminderProjection(ctx, tx, id, source)
		if readErr != nil {
			return nil, readErr
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if source.EntryVersion != in.EntryVersion || lockedVersion != in.EntryVersion || source.ModuleGeneration != in.Generation ||
		source.AccessVersion != in.AccessVersion || source.Version != in.Version {
		return nil, ErrConflict
	}
	if in.Action == "save" || in.Action == "confirm" {
		allowed, recipientErr := financeReminderRecipientAllowed(ctx, tx, id, source.ModuleGeneration, entryID, in.ResponsibleMemberID)
		if recipientErr != nil {
			return nil, recipientErr
		}
		if !allowed {
			return nil, Invalid{"Die zuständige Person benötigt aktuellen Zugriff auf die Buchung"}
		}
	}
	if err = writeFinancePaymentReminder(ctx, tx, id, entryID, source, in); err != nil {
		return nil, err
	}
	// Superseded pending revisions cannot become another delivery. The final
	// sender also revalidates source state for requests already in flight.
	_, err = tx.Exec(ctx, `WITH pending AS(SELECT id FROM core.notification_intents
 WHERE household_id=$1 AND module_id='expenses' AND resource_type='payment_reminders' AND resource_id=$2 AND state='pending'
 ORDER BY id LIMIT 256 FOR UPDATE)
 UPDATE core.notification_intents n SET state='cancelled',version=version+1,updated_at=clock_timestamp()
 FROM pending WHERE n.id=pending.id`, id.HouseholdID, entryID)
	if err != nil {
		return nil, err
	}
	current, err := readFinancePaymentReminderSource(ctx, tx, id, entryID)
	if err != nil {
		return nil, err
	}
	if current.PermissionEpoch != source.PermissionEpoch || current.EntryVersion != source.EntryVersion || current.AccessVersion != source.AccessVersion {
		return nil, ErrConflict
	}
	out, err := financePaymentReminderProjection(ctx, tx, id, current)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records
 (household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'expenses',$6,200,'{}'::jsonb)`, id.HouseholdID, id.UserID, operation, in.RequestID, hash[:], source.ModuleGeneration)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, financeAuthorityError(err)
	}
	return out, nil
}

func writeFinancePaymentReminder(ctx context.Context, tx pgx.Tx, id auth.Identity, entryID string, source financePaymentReminderSource, in FinancePaymentReminderInput) error {
	if in.Action == "save" {
		if source.Reminder == nil {
			_, err := tx.Exec(ctx, `INSERT INTO expenses.notification_payment_reminders
 (household_id,id,due_on,responsible_member_id,state,created_by) VALUES($1,$2,$3::date,$4,'draft',$5)`,
				id.HouseholdID, entryID, in.DueOn, in.ResponsibleMemberID, id.UserID)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE expenses.notification_payment_reminders SET due_on=$3::date,responsible_member_id=$4,
 state='draft',confirmed_entry_version=NULL WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID, in.DueOn, in.ResponsibleMemberID)
		return err
	}
	if source.Reminder == nil || source.DueOn != in.DueOn || source.ResponsibleMembershipID != in.ResponsibleMemberID {
		return Invalid{"Zahlungserinnerung zuerst als Entwurf speichern und erneut prüfen"}
	}
	switch in.Action {
	case "confirm":
		if source.VerifiedPaid || source.Reversed || source.Reminder.State != "draft" && !source.NeedsReconfirmation {
			return Invalid{"Nur einen ungezahlten, aktuellen Erinnerungsentwurf ausdrücklich bestätigen"}
		}
		_, err := tx.Exec(ctx, `UPDATE expenses.notification_payment_reminders SET state='confirmed',confirmed_entry_version=$3
 WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID, source.EntryVersion)
		return err
	case "complete", "cancel":
		if source.Reminder.State == "completed" || source.Reminder.State == "cancelled" {
			return ErrConflict
		}
		if in.Action == "complete" && source.Reminder.State != "confirmed" {
			return Invalid{"Nur eine bestätigte Erinnerung als erledigt markieren"}
		}
		state := "completed"
		if in.Action == "cancel" {
			state = "cancelled"
		}
		_, err := tx.Exec(ctx, `UPDATE expenses.notification_payment_reminders SET state=$3 WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID, state)
		return err
	default:
		return ErrConflict
	}
}
