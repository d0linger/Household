package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const planningPurchaseMergeMessage = "Eine Position wird durch die Mahlzeitenplanung verwaltet. Die Einkaufszuordnung zuerst ausdrücklich lösen; der Mahlzeitenbedarf bleibt aktiv."

// CheckShoppingMerge checks the typed relationship for both preview participants.
// The mutation repeats this check with row locks in its own transaction.
func (s *Store) CheckShoppingMerge(ctx context.Context, id auth.Identity, first, second string) error {
	if !value.ValidUUID(first) || !value.ValidUUID(second) || first == second {
		return Invalid{"Zwei unterschiedliche Artikel auswählen"}
	}
	var managed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.planning_purchase_requirements
 WHERE household_id=$1 AND item_id=ANY($2::uuid[]) AND procurement_disposition='managed')`, id.HouseholdID, []string{first, second}).Scan(&managed)
	if err != nil {
		return err
	}
	if managed {
		return Invalid{planningPurchaseMergeMessage}
	}
	return nil
}

func guardPlanningPurchaseMerge(ctx context.Context, tx pgx.Tx, id auth.Identity, first, second string) error {
	var requirement string
	err := tx.QueryRow(ctx, `SELECT id FROM core.planning_purchase_requirements WHERE household_id=$1 AND item_id=ANY($2::uuid[])
 AND procurement_disposition='managed' ORDER BY id LIMIT 1 FOR UPDATE NOWAIT`, id.HouseholdID, []string{first, second}).Scan(&requirement)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return shoppingContention(err)
	}
	return Invalid{planningPurchaseMergeMessage}
}
