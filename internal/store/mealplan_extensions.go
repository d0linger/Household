package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// PlanningExtensionCommand fences a reviewed seven-day planning operation.
type PlanningExtensionCommand struct {
	Generation  int64  `json:"generation"`
	PlanVersion int64  `json:"plan_version"`
	Week        string `json:"week"`
}

func planningIdentityValid(id auth.Identity) bool {
	return value.ValidUUID(id.HouseholdID) && value.ValidUUID(id.UserID) &&
		value.ValidUUID(id.MembershipID) && value.ValidUUID(id.SessionID) && id.AuthVersion > 0 &&
		(id.Role == "admin" || id.Role == "editor" || id.Role == "reader") && !id.MustChange
}

// planningExtensionRead rechecks authority inside the same bounded snapshot as
// every optional-data read. Its caller owns rollback/commit of the returned tx.
func (s *Store) planningExtensionRead(ctx context.Context, id auth.Identity, pid string) (pgx.Tx, Record, int64, error) {
	if !planningIdentityValid(id) || !value.ValidUUID(pid) {
		return nil, nil, 0, ErrForbidden
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, 0, err
	}
	var authorized bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.sessions session
 JOIN core.users u ON u.id=session.user_id
 JOIN core.memberships member ON member.user_id=u.id
 WHERE session.id=$1 AND u.id=$2 AND member.household_id=$3 AND member.id=$4
 AND member.role=$5 AND member.inactive_at IS NULL AND u.disabled_at IS NULL
 AND NOT u.must_change_password AND u.auth_version=$6 AND session.auth_version=u.auth_version
 AND session.revoked_at IS NULL AND session.idle_expires_at>now() AND session.absolute_expires_at>now()`,
		id.SessionID, id.UserID, id.HouseholdID, id.MembershipID, id.Role, id.AuthVersion).Scan(&authorized)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	if err != nil {
		rollback(tx)
		return nil, nil, 0, err
	}
	var generation int64
	err = tx.QueryRow(ctx, `SELECT data_generation FROM core.module_states
 WHERE household_id=$1 AND module_id='mealplan' AND state='enabled'`, id.HouseholdID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		rollback(tx)
		return nil, nil, 0, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT to_jsonb(p) FROM mealplan.plans p WHERE household_id=$1 AND id=$2`, id.HouseholdID, pid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		rollback(tx)
		return nil, nil, 0, err
	}
	plan, err := decode(raw)
	if err != nil {
		rollback(tx)
		return nil, nil, 0, err
	}
	return tx, plan, generation, nil
}

func planningExtensionWindow(plan Record, week string) ([]string, error) {
	days, err := planningWindow(week)
	if err != nil {
		return nil, err
	}
	if days[6] < String(plan["starts_on"]) || days[0] > String(plan["ends_on"]) {
		return nil, Invalid{"Die gewählten sieben Tage liegen außerhalb des Plans"}
	}
	return days, nil
}

func (s *Store) planningExtensionWrite(ctx context.Context, id auth.Identity, pid, key, requestID, operation string,
	base PlanningExtensionCommand, payload any, write func(context.Context, pgx.Tx, Record) (Record, error)) (Record, error) {
	return s.planningExtensionTransaction(ctx, id, pid, key, requestID, operation, base, payload, false, write)
}

// calendarOnly permits an explicit Reader-owned sharing capability; it never
// grants access to ordinary planning writes or advances the plan's content.
func (s *Store) planningExtensionTransaction(ctx context.Context, id auth.Identity, pid, key, requestID, operation string,
	base PlanningExtensionCommand, payload any, calendarOnly bool,
	write func(context.Context, pgx.Tx, Record) (Record, error)) (Record, error) {
	if !planningIdentityValid(id) || !calendarOnly && !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(pid) || !value.ValidUUID(key) || base.Generation <= 0 || base.PlanVersion <= 0 {
		return nil, Invalid{"Plan, Generation, Versionsstand und gültige Vorgangskennung prüfen"}
	}
	if _, err := planningWindow(base.Week); err != nil {
		return nil, err
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	var tx pgx.Tx
	var err error
	if calendarOnly {
		tx, err = audit.Begin(ctx, s.Pool)
		if err == nil {
			err = checkSession(ctx, tx, id)
		}
		if err == nil {
			var current int64
			err = tx.QueryRow(ctx, `SELECT data_generation FROM core.module_states
 WHERE household_id=$1 AND module_id='mealplan' AND state='enabled' FOR SHARE`, id.HouseholdID).Scan(&current)
			if errors.Is(err, pgx.ErrNoRows) {
				err = ErrNotFound
			} else if err == nil && current != base.Generation {
				err = ErrConflict
			}
		}
	} else {
		tx, err = s.BeginMutation(ctx, id, "mealplan", base.Generation)
	}
	if err != nil {
		if tx != nil {
			rollback(tx)
		}
		return nil, err
	}
	defer rollback(tx)
	var currentIdentity bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.users u JOIN core.memberships m ON m.user_id=u.id
 WHERE u.id=$1 AND m.id=$2 AND m.household_id=$3 AND m.inactive_at IS NULL
 AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=$4 AND m.role=$5`,
		id.UserID, id.MembershipID, id.HouseholdID, id.AuthVersion, id.Role).Scan(&currentIdentity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	op := "mealplan/extension/" + operation + "/" + pid
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id.UserID+op+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT to_jsonb(p) FROM mealplan.plans p
 WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT`, id.HouseholdID, pid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	plan, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if Int(plan["version"]) != base.PlanVersion {
		return nil, PlanningConflict{"Der Plan wurde geändert. Den gespeicherten Stand laden und die Vorschau erneut prüfen."}
	}
	if _, err = planningExtensionWindow(plan, base.Week); err != nil {
		return nil, err
	}
	result, err := write(ctx, tx, plan)
	if err != nil {
		return nil, shoppingContention(err)
	}
	if result == nil {
		return nil, errors.New("planning extension returned no result")
	}
	if !calendarOnly {
		if _, err = tx.Exec(ctx, `UPDATE mealplan.plans SET updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, pid); err != nil {
			return nil, err
		}
		if err = s.projectPlanning(ctx, tx, id, "plans", pid, base.Generation); err != nil {
			return nil, err
		}
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version FROM mealplan.plans WHERE household_id=$1 AND id=$2`, id.HouseholdID, pid).Scan(&version); err != nil {
		return nil, err
	}
	result["plan_version"], result["generation"] = version, base.Generation
	if err = Audit(ctx, tx, id, "mealplan", "plans", pid, "record_write", requestID); err != nil {
		return nil, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records
 (household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'mealplan',$6,200,$7)`, id.HouseholdID, id.UserID, op, key, hash[:], base.Generation, response); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}
