package store

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"

	"household/internal/audit"

	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) shoppingPruneWatches(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT household_id FROM core.module_states WHERE module_id='shopping' AND state='enabled'
 AND settings ? 'shopping_notify_v1' ORDER BY household_id LIMIT 4`)
	if err != nil {
		return err
	}
	households := []string{}
	for rows.Next() {
		var h string
		if err = rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		households = append(households, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, h := range households {
		if err = s.shoppingPruneHousehold(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) shoppingPruneHousehold(ctx context.Context, household string) error {
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer shoppingRollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", household); err != nil {
		return err
	}
	var generation int64
	var enabled bool
	err = tx.QueryRow(ctx, "SELECT data_generation,state='enabled' FROM core.module_states WHERE household_id=$1 AND module_id='shopping' FOR UPDATE NOWAIT", household).Scan(&generation, &enabled)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "55P03" {
			return nil
		}
		return err
	}
	if !enabled {
		return nil
	}
	settings, err := shoppingReadSettings(ctx, tx, household, generation)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, u := range settings.Users {
		for lid := range u.Watches {
			ids = append(ids, lid)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, "SELECT id FROM shopping.lists WHERE household_id=$1 AND id=ANY($2::uuid[])", household, ids)
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for rows.Next() {
		var lid string
		if err = rows.Scan(&lid); err != nil {
			rows.Close()
			return err
		}
		exists[lid] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	keys := []string{}
	for uid, u := range settings.Users {
		for lid := range u.Watches {
			if !exists[lid] {
				keys = append(keys, uid+"/"+lid)
			}
		}
	}
	sort.Strings(keys)
	changed := map[string]bool{}
	for _, key := range keys[:min(20, len(keys))] {
		uid, lid := key[:36], key[37:]
		var pending bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.outbox_events e LEFT JOIN core.event_deliveries d
 ON d.event_id=e.id AND d.consumer_id='shopping-list-notifications-v1'
 WHERE e.household_id=$1 AND e.module_id='shopping' AND e.module_generation=$2 AND e.event_version=2
 AND e.payload->'shopping'->'list_ids' ? $3 AND (d.event_id IS NULL OR d.state<>'done'))
 OR EXISTS(SELECT 1 FROM core.jobs WHERE household_id=$1 AND module_id='shopping' AND module_generation=$2
 AND kind IN('shopping_digest_v1','shopping_push_v1') AND payload->>'list_id'=$3 AND state IN('pending','running','paused'))
 OR EXISTS(SELECT 1 FROM core.notifications WHERE household_id=$1 AND user_id=$4 AND module_id='shopping'
 AND kind='shopping_list_changed_v1' AND dismissed_at IS NULL AND dedupe_key LIKE $5)`, household, generation, lid, uid,
			"shopping-notice-v1/"+household+"/"+uid+"/"+strconv.FormatInt(generation, 10)+"/"+lid+"/%/deleted").Scan(&pending)
		if err != nil {
			return err
		}
		if pending {
			continue
		}
		u := settings.Users[uid]
		rev, _ := shoppingRevision(u.Revision)
		if rev == math.MaxUint64 {
			continue
		}
		delete(u.Watches, lid)
		settings.Users[uid] = u
		changed[uid] = true
	}
	if len(changed) == 0 {
		return nil
	}
	for uid := range changed {
		u := settings.Users[uid]
		rev, _ := shoppingRevision(u.Revision)
		u.Revision = strconv.FormatUint(rev+1, 10)
		settings.Users[uid] = u
	}
	err = shoppingWriteSettings(ctx, tx, household, settings)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
