package store

import (
	"context"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PlanningPurchaseDestination(ctx context.Context, id auth.Identity, listID string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if !value.ValidUUID(listID) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('id',l.id,'name',l.name,'version',l.version,'generation',ms.data_generation,
 'open',l.archived_at IS NULL AND NOT l.is_template AND NOT EXISTS(SELECT 1 FROM shopping.checkouts c WHERE c.household_id=l.household_id AND c.list_id=l.id))
 FROM shopping.lists l JOIN core.module_states ms ON ms.household_id=l.household_id AND ms.module_id='shopping' AND ms.state='enabled'
 WHERE l.household_id=$1 AND l.id=$2`, id.HouseholdID, listID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}
