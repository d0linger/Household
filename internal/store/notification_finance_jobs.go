package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

type financeNotificationCursor struct {
	Schema int    `json:"schema"`
	Phase  string `json:"phase"`
	After  string `json:"after"`
}

func financeNotificationScanKey(household, user string, generation int64) string {
	return "finance-scan-v1/" + household + "/" + user + "/" + strconv.FormatInt(generation, 10)
}

func createFinanceNotificationScan(ctx context.Context, tx pgx.Tx, household, user string, generation int64) (string, error) {
	key := financeNotificationScanKey(household, user, generation)
	_, err := tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key)
 VALUES($1,'expenses',$2,$3,'{"schema":1,"phase":"payments","after":""}',$4) ON CONFLICT(dedupe_key) DO NOTHING`, household, generation, FinanceNotificationScanJobKind, key)
	return key, err
}

// DeferFinanceNotificationScan records a bounded retry for a user whose opted-in
// devices no longer have a usable registering session, preserving fair rotation.
func (s *Store) DeferFinanceNotificationScan(ctx context.Context, tx pgx.Tx, household, user string) error {
	var generation int64
	err := tx.QueryRow(ctx, `SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id='expenses' AND state='enabled' FOR SHARE`, household).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key, err := createFinanceNotificationScan(ctx, tx, household, user, generation)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core.jobs SET available_at=clock_timestamp()+interval '1 minute' WHERE dedupe_key=$1 AND kind=$2 AND state='pending'`, key, FinanceNotificationScanJobKind)
	return err
}

// StageFinanceNotificationPage runs inside the caller's repeatable-read audit
// transaction. Its cursor, observations and new intents commit atomically.
func (s *Store) StageFinanceNotificationPage(ctx context.Context, tx pgx.Tx, id auth.Identity, limit int) (int, error) {
	limit = max(1, min(limit, 10)) // Each budget may inspect at most120 authorized months.
	var isolation string
	if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
		return 0, err
	}
	if isolation != "repeatable read" {
		return 0, ErrConflict
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, id.HouseholdID); err != nil {
		return 0, err
	}
	c, err := readFinanceNotificationContext(ctx, tx, id)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `SELECT state FROM core.module_states WHERE household_id=$1 AND module_id='expenses' AND state='enabled' AND data_generation=$2 FOR SHARE`, id.HouseholdID, c.scope.generation); err != nil {
		return 0, err
	}
	key, err := createFinanceNotificationScan(ctx, tx, id.HouseholdID, id.UserID, c.scope.generation)
	if err != nil {
		return 0, err
	}
	var job string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT id::text,CASE WHEN octet_length(payload::text)<=512 THEN payload END FROM core.jobs
 WHERE dedupe_key=$1 AND kind=$2 AND payload_version=1 AND state='pending' AND available_at<=clock_timestamp() FOR UPDATE SKIP LOCKED`, key, FinanceNotificationScanJobKind).Scan(&job, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cursor financeNotificationCursor
	if shoppingDecodeClosed(raw, &cursor) != nil || cursor.Schema != 1 || cursor.Phase != "payments" && cursor.Phase != "budgets" || cursor.After != "" && !shoppingCanonicalUUID(cursor.After) {
		return 0, ErrConflict
	}
	kind, table := "entries", "notification_payment_reminders"
	if cursor.Phase == "budgets" {
		kind, table = "category_budgets", "category_budgets"
	}
	predicate, args, err := financeResourcePredicate(c.scope, kind, "view", "t", 4)
	if err != nil {
		return 0, err
	}
	// Only the two compiled table names above reach this identifier boundary.
	rows, err := tx.Query(ctx, `SELECT t.id::text FROM `+pgx.Identifier{"expenses", table}.Sanitize()+` t
 WHERE t.household_id=$1 AND(nullif($2,'')::uuid IS NULL OR t.id>nullif($2,'')::uuid)
 AND `+predicate+` ORDER BY t.id LIMIT $3`, append([]any{id.HouseholdID, cursor.After, limit + 1}, args...)...)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var rid string
		if err = rows.Scan(&rid); err != nil {
			break
		}
		ids = append(ids, rid)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	zone, err := time.LoadLocation(c.scope.timezone)
	if err != nil {
		return 0, err
	}
	for _, rid := range ids[:min(limit, len(ids))] {
		var sampled time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&sampled); err != nil {
			return 0, err
		}
		if cursor.Phase == "payments" {
			source, e := readFinancePaymentReminderSource(ctx, tx, id, rid)
			if e != nil {
				return 0, e
			}
			intents, e := financePaymentIntents(id, source, sampled)
			if e != nil {
				return 0, e
			}
			for _, i := range intents {
				if err = s.stageFinanceNotificationIntent(ctx, tx, id, i); err != nil {
					return 0, err
				}
			}
		} else {
			month := sampled.In(zone).Format("2006-01")
			budget, e := readFinanceNotificationBudget(ctx, tx, id, c, rid, month)
			if e != nil {
				return 0, e
			}
			for _, threshold := range []int{80, 100} {
				i, e := observeFinanceBudget(ctx, tx, id, c, rid, month, budget, threshold, sampled)
				if e != nil {
					return 0, e
				}
				if i != nil {
					if err = s.stageFinanceNotificationIntent(ctx, tx, id, *i); err != nil {
						return 0, err
					}
				}
			}
		}
		cursor.After = rid
	}
	completed := false
	if len(ids) <= limit {
		cursor.After = ""
		if cursor.Phase == "payments" {
			cursor.Phase = "budgets"
		} else {
			cursor.Phase = "payments"
			completed = true
		}
	}
	raw, err = json.Marshal(cursor)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE core.jobs SET payload=$2,available_at=clock_timestamp()+CASE WHEN $3 THEN interval '1 minute' ELSE interval '0' END,
 attempts=0,last_error=NULL WHERE id=$1`, job, raw, completed)
	return 1, err
}

func (s *Store) stageFinanceNotificationIntent(ctx context.Context, tx pgx.Tx, id auth.Identity, i notification.Intent) error {
	cutoff, err := s.FinanceNotificationAuthority(ctx, tx, id, i)
	if err != nil || cutoff.IsZero() {
		return err
	}
	var consent bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.notification_preferences WHERE household_id=$1 AND user_id=$2 AND category='expenses'
 AND enabled AND enabled_since<=$3 FOR SHARE`, i.HouseholdID, i.UserID, cutoff).Scan(&consent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = EnqueueNotificationIntent(ctx, tx, i)
	return err
}
