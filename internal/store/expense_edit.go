package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type ExpenseEditInput struct {
	expenseedit.Input
	Baseline []expenseedit.LineVersion `json:"baseline"`
	Removed  []expenseedit.LineVersion `json:"removed"`
}

// ExpenseEditReceipt records acceptance, never an obsolete private form body.
// Fetch a fresh ExpenseBundle before offering another editable baseline.
type ExpenseEditReceipt struct {
	ID             string    `json:"id"`
	EntryID        string    `json:"entry_id"`
	Action         string    `json:"action"`
	SavedVersion   int64     `json:"saved_version"`
	CreatedAt      time.Time `json:"created_at"`
	Replayed       bool      `json:"replayed"`
	CurrentExists  bool      `json:"current_exists"`
	CurrentVersion int64     `json:"current_version"`
	CurrentState   string    `json:"current_state"`
}

func (s *Store) SaveExpense(ctx context.Context, id auth.Identity, key, requestID string, input ExpenseEditInput) (ExpenseEditReceipt, error) {
	if !id.CanWrite() || !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) ||
		!value.ValidUUID(id.MembershipID) || !value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return ExpenseEditReceipt{}, ErrForbidden
	}
	if !value.ValidUUID(key) || !value.ValidUUID(requestID) || len(input.Baseline) > expenseedit.LineLimit || len(input.Removed) > expenseedit.LineLimit {
		return ExpenseEditReceipt{}, Invalid{"Gültigen Vorgang und vollständige begrenzte Zeilenbasis prüfen"}
	}
	draft, err := expenseedit.Normalize(input.Input)
	if err != nil {
		return ExpenseEditReceipt{}, err
	}
	input.Baseline, err = canonicalExpenseBasis(input.Baseline)
	if err != nil {
		return ExpenseEditReceipt{}, err
	}
	input.Removed, err = canonicalExpenseBasis(input.Removed)
	if err != nil {
		return ExpenseEditReceipt{}, err
	}
	key = strings.ToLower(key)
	encoded, err := json.Marshal(struct {
		Draft    expenseedit.Draft
		Baseline []expenseedit.LineVersion
		Removed  []expenseedit.LineVersion
	}{draft, input.Baseline, input.Removed})
	if err != nil {
		return ExpenseEditReceipt{}, err
	}
	hash := sha256.Sum256(encoded)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "expenses", draft.Generation)
	if err != nil {
		return ExpenseEditReceipt{}, err
	}
	defer rollback(tx)
	var scope ExpenseBundle
	if err = readExpenseBundleScope(ctx, tx, id, &scope); err != nil {
		return ExpenseEditReceipt{}, err
	}
	if scope.Currency != "EUR" {
		return ExpenseEditReceipt{}, Invalid{"Dieser Editor erfasst ausdrücklich EUR. Haushaltswährung prüfen."}
	}
	// Keys survive response pruning and module purge; authorization and current
	// generation are established before reading any prior operation identity.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.HouseholdID+id.UserID+"expense-edit/"+key); err != nil {
		return ExpenseEditReceipt{}, err
	}
	prior, priorHash, err := readExpenseEditProof(ctx, tx, id, key)
	if err == nil {
		if !bytes.Equal(hash[:], priorHash) {
			return ExpenseEditReceipt{}, ErrConflict
		}
		if _, err = lockFinancialPolicy(ctx, tx, id, "entries", prior.EntryID, financialEdit); err != nil {
			return ExpenseEditReceipt{}, shoppingContention(err)
		}
		prior.Replayed = true
		if err = currentExpenseReceipt(ctx, tx, id, scope.Generation, &prior); err != nil {
			return ExpenseEditReceipt{}, err
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExpenseEditReceipt{}, err
	}
	if draft.EntryID != "" {
		if _, err = lockFinancialPolicy(ctx, tx, id, "entries", draft.EntryID, financialEdit); err != nil {
			return ExpenseEditReceipt{}, shoppingContention(err)
		}
	}
	current, payer, err := lockExpenseEdit(ctx, tx, id, draft)
	if err != nil {
		return ExpenseEditReceipt{}, shoppingContention(err)
	}
	if draft.EntryID != "" {
		// RC takes a new snapshot after parent/child locks: the complete binding
		// set must still agree before reconciling or exposing financial facts.
		currentScope, scopeErr := readFinancialScope(ctx, tx, id)
		if scopeErr != nil {
			return ExpenseEditReceipt{}, scopeErr
		}
		if _, err = readFinancialAccessVersion(ctx, tx, currentScope, draft.EntryID); err != nil {
			return ExpenseEditReceipt{}, err
		}
	}
	versions := make([]expenseedit.LineVersion, 0, len(current))
	for _, line := range current {
		versions = append(versions, line.LineVersion)
	}
	changes, err := expenseedit.ReconcileLines(draft, input.Baseline, input.Removed, versions)
	if errors.Is(err, expenseedit.ErrStaleLines) {
		return ExpenseEditReceipt{}, ErrConflict
	}
	if err != nil {
		return ExpenseEditReceipt{}, err
	}
	if err = validateExpenseSelections(ctx, tx, id, draft, current, payer); err != nil {
		return ExpenseEditReceipt{}, shoppingContention(err)
	}
	entry, err := s.writeExpenseEdit(ctx, tx, id, draft, changes, requestID)
	if err != nil {
		return ExpenseEditReceipt{}, shoppingContention(err)
	}
	receipt := ExpenseEditReceipt{ID: value.UUID(), EntryID: String(entry["id"]), Action: draft.Action, SavedVersion: Int(entry["version"])}
	err = tx.QueryRow(ctx, `INSERT INTO core.expense_edit_operations(id,household_id,created_by,request_key,request_hash,
 expenses_generation,operation,entry_id,result_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`,
		receipt.ID, id.HouseholdID, id.UserID, key, hash[:], draft.Generation, draft.Action, receipt.EntryID, receipt.SavedVersion).Scan(&receipt.CreatedAt)
	if err != nil {
		return ExpenseEditReceipt{}, shoppingContention(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ExpenseEditReceipt{}, shoppingContention(err)
	}
	receipt.CurrentExists, receipt.CurrentVersion, receipt.CurrentState = true, receipt.SavedVersion, String(entry["state"])
	return receipt, nil
}

func readExpenseEditProof(ctx context.Context, tx pgx.Tx, id auth.Identity, key string) (ExpenseEditReceipt, []byte, error) {
	var out ExpenseEditReceipt
	var hash []byte
	err := tx.QueryRow(ctx, `SELECT id,entry_id,operation,result_version,created_at,request_hash FROM core.expense_edit_operations
 WHERE household_id=$1 AND created_by=$2 AND request_key=$3`, id.HouseholdID, id.UserID, key).Scan(
		&out.ID, &out.EntryID, &out.Action, &out.SavedVersion, &out.CreatedAt, &hash)
	return out, hash, err
}

func currentExpenseReceipt(ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64, out *ExpenseEditReceipt) error {
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return err
	}
	if scope.generation != generation {
		return ErrNotFound
	}
	predicate, args, err := financialPredicate(scope, "entries", financialEdit, 3)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, "SELECT t.version,t.state FROM expenses.entries t WHERE t.household_id=$1 AND t.id=$2 AND "+predicate,
		append([]any{id.HouseholdID, out.EntryID}, args...)...).Scan(&out.CurrentVersion, &out.CurrentState)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err == nil {
		out.CurrentExists = true
	}
	return err
}
