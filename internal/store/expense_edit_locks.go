package store

import (
	"context"
	"errors"
	"slices"

	"household/internal/auth"
	"household/internal/expenseedit"

	"github.com/jackc/pgx/v5"
)

type expenseEditCurrentLine struct {
	expenseedit.LineVersion
	CategoryID string
}

func lockExpenseEdit(ctx context.Context, tx pgx.Tx, id auth.Identity, draft expenseedit.Draft) ([]expenseEditCurrentLine, string, error) {
	current := []expenseEditCurrentLine{}
	if draft.EntryID == "" {
		return current, "", nil
	}
	var state string
	var version int64
	var payer *string
	err := tx.QueryRow(ctx, "SELECT state,version,paid_by_membership_id FROM expenses.entries WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, draft.EntryID).Scan(&state, &version, &payer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if state != "draft" || version != draft.Version {
		return nil, "", ErrConflict
	}
	// Establish the complete bounded set using the editor's index before UUID
	// lock ordering. The parent lock excludes all App child changes via NOWAIT.
	rows, err := tx.Query(ctx, "SELECT id FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2 ORDER BY position,id LIMIT 101", id.HouseholdID, draft.EntryID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	candidates := make([]string, 0, expenseedit.LineLimit+1)
	for rows.Next() {
		var candidate string
		if err = rows.Scan(&candidate); err != nil {
			return nil, "", err
		}
		candidates = append(candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	rows.Close()
	if len(candidates) > expenseedit.LineLimit {
		return nil, "", Invalid{"Mehr als 100 gespeicherte Zeilen: Einzelverwaltung oder Export prüfen; nichts wird gekürzt."}
	}
	rows, err = tx.Query(ctx, `SELECT id,version,category_id FROM expenses.entry_lines
 WHERE household_id=$1 AND entry_id=$2 AND id=ANY($3::uuid[]) ORDER BY id FOR UPDATE NOWAIT`, id.HouseholdID, draft.EntryID, candidates)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var line expenseEditCurrentLine
		if err = rows.Scan(&line.ID, &line.Version, &line.CategoryID); err != nil {
			return nil, "", err
		}
		current = append(current, line)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(current) != len(candidates) {
		return nil, "", ErrConflict
	}
	previousPayer := ""
	if payer != nil {
		previousPayer = *payer
	}
	return current, previousPayer, nil
}

func validateExpenseSelections(ctx context.Context, tx pgx.Tx, id auth.Identity, draft expenseedit.Draft, current []expenseEditCurrentLine, previousPayer string) error {
	categoryIDs := make([]string, 0, len(draft.Lines))
	for _, line := range draft.Lines {
		categoryIDs = append(categoryIDs, line.CategoryID)
	}
	slices.Sort(categoryIDs)
	categoryIDs = slices.Compact(categoryIDs)
	rows, err := tx.Query(ctx, "SELECT id,archived_at IS NULL FROM expenses.categories WHERE household_id=$1 AND id=ANY($2::uuid[]) ORDER BY id FOR SHARE NOWAIT", id.HouseholdID, categoryIDs)
	if err != nil {
		return err
	}
	defer rows.Close()
	categories := make(map[string]bool, len(categoryIDs))
	for rows.Next() {
		var category string
		var active bool
		if err = rows.Scan(&category, &active); err != nil {
			return err
		}
		categories[category] = active
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	previous := make(map[string]string, len(current))
	for _, line := range current {
		previous[line.ID] = line.CategoryID
	}
	for _, line := range draft.Lines {
		active, exists := categories[line.CategoryID]
		if !exists || !active && (draft.Action == "post" || line.ID == "" || previous[line.ID] != line.CategoryID) {
			return expenseedit.Problems{"lines": "Aktive Haushaltskategorien auswählen. Archivierte Zuordnungen bleiben nur im unveränderten Entwurf erhalten."}
		}
	}
	if draft.Header.PaidByMembershipID == "" {
		return nil
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT m.inactive_at IS NULL AND u.disabled_at IS NULL FROM core.memberships m
 JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.id=$2 FOR SHARE OF m,u NOWAIT`, id.HouseholdID, draft.Header.PaidByMembershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active && (draft.Action == "post" || previousPayer != draft.Header.PaidByMembershipID) {
		return expenseedit.Problems{"header.paid_by": "Aktives Haushaltsmitglied auswählen oder Zahlenden offen lassen. Inaktive Zahlende bleiben nur im bisherigen Entwurf erhalten."}
	}
	return err
}
