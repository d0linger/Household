package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const expenseBundleBytes = 1 << 20

type ExpenseHeader struct {
	ID                 string     `json:"id"`
	Version            int64      `json:"version"`
	State              string     `json:"state"`
	OccurredOn         string     `json:"occurred_on"`
	Kind               string     `json:"kind"`
	Description        string     `json:"description"`
	Payee              string     `json:"payee"`
	AmountMinor        string     `json:"amount_minor"`
	PaidByMembershipID *string    `json:"paid_by_membership_id"`
	PayerLabel         string     `json:"payer_label"`
	PayerActive        bool       `json:"payer_active"`
	PostedAt           *time.Time `json:"posted_at"`
	ReversesEntryID    *string    `json:"reverses_entry_id"`
	ReplacesEntryID    *string    `json:"replaces_entry_id"`
}

type ExpenseBundleLine struct {
	ID               string `json:"id"`
	Key              string `json:"key"`
	Version          int64  `json:"version"`
	Position         int    `json:"position"`
	CategoryID       string `json:"category_id"`
	CategoryLabel    string `json:"category_label"`
	CategoryArchived bool   `json:"category_archived"`
	CategoryMissing  bool   `json:"category_missing"`
	AmountMinor      string `json:"amount_minor"`
	Note             string `json:"note"`
}

type ExpenseBundle struct {
	Advanced       bool                      `json:"advanced"`
	FinanceURL     string                    `json:"finance_url,omitempty"`
	Generation     int64                     `json:"generation"`
	AccessVersion  int64                     `json:"access_version"`
	ReadAt         time.Time                 `json:"read_at"`
	Timezone       string                    `json:"timezone"`
	Today          string                    `json:"today"`
	Currency       string                    `json:"currency"`
	Header         ExpenseHeader             `json:"header"`
	Lines          []ExpenseBundleLine       `json:"lines"`
	Baseline       []expenseedit.LineVersion `json:"baseline"`
	Editable       bool                      `json:"editable"`
	LockReason     string                    `json:"lock_reason,omitempty"`
	SumMinor       string                    `json:"sum_minor"`
	RemainingMinor *string                   `json:"remaining_minor"`
}

// ExpenseBundle reads shared household facts without normalizing historical
// posted/reversal content into an editable draft. The HTTP caller holds Gate.
func (s *Store) ExpenseBundle(ctx context.Context, id auth.Identity, entry string) (ExpenseBundle, error) {
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) || !value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return ExpenseBundle{}, ErrForbidden
	}
	if entry != "" && !value.ValidUUID(entry) {
		return ExpenseBundle{}, Invalid{"Gültige Buchungskennung erforderlich"}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ExpenseBundle{}, err
	}
	defer rollback(tx)
	out := ExpenseBundle{Lines: []ExpenseBundleLine{}, Baseline: []expenseedit.LineVersion{}, SumMinor: "0"}
	if err = readExpenseBundleScope(ctx, tx, id, &out); err != nil {
		return ExpenseBundle{}, err
	}
	zone, err := time.LoadLocation(out.Timezone)
	if err != nil {
		return ExpenseBundle{}, Invalid{"Gültige Haushaltszeitzone erforderlich"}
	}
	out.Today = out.ReadAt.In(zone).Format(time.DateOnly)
	out.Editable = id.CanWrite()
	if !out.Editable {
		out.LockReason = "Du hast Lesezugriff auf diese Haushaltsbuchung."
	}
	if out.Currency != "EUR" {
		out.Editable, out.LockReason = false, "Dieser Editor unterstützt ausdrücklich EUR. Haushaltswährung vor der Erfassung prüfen."
	}
	if entry == "" {
		out.Header = ExpenseHeader{OccurredOn: out.Today, Kind: "expense", State: "draft"}
	} else {
		scope, scopeErr := readFinancialScope(ctx, tx, id)
		if scopeErr != nil {
			return ExpenseBundle{}, scopeErr
		}
		out.AccessVersion, err = readFinancialAccessVersion(ctx, tx, scope, entry)
		if err != nil {
			return ExpenseBundle{}, err
		}
		access, accessErr := financeAccessProjection(ctx, tx, id, scope, "entries", entry)
		if accessErr != nil {
			return ExpenseBundle{}, accessErr
		}
		canEdit, _ := access["can_edit"].(bool)
		if !canEdit {
			out.Editable = false
			out.LockReason = "Du hast Lesezugriff auf diese Buchung."
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2)`, id.HouseholdID, entry).Scan(&out.Advanced); err != nil {
			return ExpenseBundle{}, err
		}
		if out.Advanced {
			out.Header.ID = entry
			out.Editable = false
			out.LockReason = "Diese Buchung mit Finanzdetails im Finanzarbeitsbereich prüfen."
			out.FinanceURL = "/m/expenses/workspace/entries?id=" + entry
			if err = tx.Commit(ctx); err != nil {
				return ExpenseBundle{}, err
			}
			return out, nil
		}
		if err = readExpenseBundleContent(ctx, tx, id, entry, &out); err != nil {
			return ExpenseBundle{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ExpenseBundle{}, err
	}
	return out, nil
}

func readExpenseBundleScope(ctx context.Context, tx pgx.Tx, id auth.Identity, out *ExpenseBundle) error {
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return err
	}
	out.Timezone, out.Currency, out.ReadAt, out.Generation = scope.timezone, scope.currency, scope.readAt, scope.generation
	return nil
}

func readExpenseBundleContent(ctx context.Context, tx pgx.Tx, id auth.Identity, entry string, out *ExpenseBundle) error {
	h := &out.Header
	var payeeBytes, payeeChars int64
	err := tx.QueryRow(ctx, `SELECT e.id,e.version,e.state,e.occurred_on::text,e.kind,e.description,e.amount_minor::text,
 e.paid_by_membership_id,e.posted_at,e.reverses_entry_id,e.replaces_entry_id,octet_length(e.payee),
 CASE WHEN octet_length(e.payee)<=$3 THEN length(e.payee) ELSE 0 END,
 CASE WHEN u.id IS NULL THEN '' WHEN octet_length(u.display_name)<=400 AND u.display_name<>'' THEN u.display_name
 WHEN octet_length(u.username)<=256 THEN u.username ELSE 'Gespeichertes Mitglied (Name zu lang)' END,
 m.id IS NOT NULL AND m.inactive_at IS NULL AND u.id IS NOT NULL AND u.disabled_at IS NULL
 FROM expenses.entries e LEFT JOIN core.memberships m ON m.household_id=e.household_id AND m.id=e.paid_by_membership_id
 LEFT JOIN core.users u ON u.id=m.user_id WHERE e.household_id=$1 AND e.id=$2`, id.HouseholdID, entry, expenseBundleBytes).Scan(
		&h.ID, &h.Version, &h.State, &h.OccurredOn, &h.Kind, &h.Description, &h.AmountMinor,
		&h.PaidByMembershipID, &h.PostedAt, &h.ReversesEntryID, &h.ReplacesEntryID, &payeeBytes, &payeeChars, &h.PayerLabel, &h.PayerActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if payeeBytes > expenseBundleBytes {
		return Invalid{"Gespeicherter Empfänger überschreitet die begrenzte Editorgröße. Einzelverwaltung oder Export verwenden; es wird nichts gekürzt."}
	}
	if h.State != "draft" || h.Kind != "expense" && h.Kind != "refund" {
		out.Editable, out.LockReason = false, "Gebuchte Beträge und Stornos bleiben unverändert."
	}
	if payeeChars > expenseedit.PayeeLimit || h.Version <= 0 {
		out.Editable, out.LockReason = false, "Gespeicherte Feldlänge oder Version liegt außerhalb der Editorgrenzen. Einzelverwaltung prüfen."
	}
	if err = readExpenseBundleLines(ctx, tx, id, entry, payeeBytes, out); err != nil {
		return err
	}
	// Read unbounded legacy text only after count and aggregate byte preflight.
	if err = tx.QueryRow(ctx, "SELECT payee FROM expenses.entries WHERE household_id=$1 AND id=$2", id.HouseholdID, entry).Scan(&h.Payee); err != nil {
		return err
	}
	return readExpenseBundleNotes(ctx, tx, id, out)
}

func readExpenseBundleLines(ctx context.Context, tx pgx.Tx, id auth.Identity, entry string, textBytes int64, out *ExpenseBundle) error {
	rows, err := tx.Query(ctx, `SELECT l.id,l.version,l.position,l.category_id,coalesce(c.name,'Gespeicherte Kategorie nicht verfügbar'),
 c.archived_at IS NOT NULL,c.id IS NULL,l.amount_minor::text,octet_length(l.note),
 CASE WHEN octet_length(l.note)<=$3 THEN length(l.note) ELSE 0 END
 FROM expenses.entry_lines l LEFT JOIN expenses.categories c ON c.household_id=l.household_id AND c.id=l.category_id
 WHERE l.household_id=$1 AND l.entry_id=$2 ORDER BY l.position,l.id LIMIT 101`, id.HouseholdID, entry, expenseBundleBytes)
	if err != nil {
		return err
	}
	defer rows.Close()
	var sum int64
	for rows.Next() {
		var line ExpenseBundleLine
		var noteBytes, noteChars int64
		if err = rows.Scan(&line.ID, &line.Version, &line.Position, &line.CategoryID, &line.CategoryLabel, &line.CategoryArchived, &line.CategoryMissing, &line.AmountMinor, &noteBytes, &noteChars); err != nil {
			return err
		}
		if len(out.Lines) == expenseedit.LineLimit {
			return Invalid{"Buchung enthält mehr als100 Kategoriezeilen. Einzelverwaltung oder Export verwenden; keine gekürzte Bearbeitungsbasis wird angeboten."}
		}
		textBytes += noteBytes
		if textBytes > expenseBundleBytes {
			return Invalid{"Gespeicherte Notizen überschreiten die begrenzte Editorgröße. Einzelverwaltung oder Export verwenden; es wird nichts gekürzt."}
		}
		if noteChars > expenseedit.NoteLimit || line.Version <= 0 {
			out.Editable, out.LockReason = false, "Gespeicherte Feldlänge oder Version liegt außerhalb der Editorgrenzen. Einzelverwaltung prüfen."
		}
		if line.CategoryMissing {
			out.Editable, out.LockReason = false, "Gespeicherte Kategorie nicht verfügbar. Zuordnung vor weiterer Bearbeitung prüfen."
		}
		amount, parseErr := strconv.ParseInt(line.AmountMinor, 10, 64)
		if parseErr != nil {
			return parseErr
		}
		sum += amount // At most100 separately constrained +/-1e12-cent rows.
		line.Key = line.ID
		out.Lines = append(out.Lines, line)
		out.Baseline = append(out.Baseline, expenseedit.LineVersion{ID: line.ID, Version: line.Version})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	total, err := strconv.ParseInt(out.Header.AmountMinor, 10, 64)
	if err != nil {
		return err
	}
	remaining := strconv.FormatInt(total-sum, 10)
	out.SumMinor, out.RemainingMinor = strconv.FormatInt(sum, 10), &remaining
	return nil
}

func readExpenseBundleNotes(ctx context.Context, tx pgx.Tx, id auth.Identity, out *ExpenseBundle) error {
	if len(out.Lines) == 0 {
		return nil
	}
	ids, positions := make([]string, 0, len(out.Lines)), make(map[string]int, len(out.Lines))
	for index, line := range out.Lines {
		ids, positions[line.ID] = append(ids, line.ID), index
	}
	rows, err := tx.Query(ctx, "SELECT id,note FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2 AND id=ANY($3::uuid[])", id.HouseholdID, out.Header.ID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var rowID, note string
		if err = rows.Scan(&rowID, &note); err != nil {
			return err
		}
		index, found := positions[rowID]
		if !found {
			return ErrConflict
		}
		out.Lines[index].Note = note
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != len(out.Lines) {
		return ErrConflict
	}
	return nil
}
