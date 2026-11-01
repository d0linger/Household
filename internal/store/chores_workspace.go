package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const choreOperationTimeout = 20 * time.Second
const choreReadTimeout = 8 * time.Second
const choreMaximumActiveSeries = 500
const choreGenerationBatch = 64

type ChoreCommand struct {
	Generation int64 `json:"generation"`
	Version    int64 `json:"version"`
}

type ChoreChecklistInput struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Required bool   `json:"required"`
}

type ChoreMemberInput struct {
	MembershipID string `json:"membership_id"`
	Eligible     bool   `json:"eligible"`
}

type ChoreSeriesInput struct {
	ChoreCommand
	ID               string                `json:"id,omitempty"`
	BaseVersion      int64                 `json:"base_version"`
	Title            string                `json:"title"`
	Description      string                `json:"description"`
	Materials        []string              `json:"materials"`
	AreaID           string                `json:"area_id"`
	EstimatedMinutes *int                  `json:"estimated_minutes"`
	Difficulty       *int                  `json:"difficulty"`
	Priority         int                   `json:"priority"`
	Schedule         chore.Schedule        `json:"schedule"`
	AssignmentKind   string                `json:"assignment_kind"`
	AssigneeID       string                `json:"assignee_id"`
	Members          []ChoreMemberInput    `json:"members"`
	Checklist        []ChoreChecklistInput `json:"checklist"`
	PrerequisiteIDs  []string              `json:"prerequisite_ids"`
	EffectiveOn      string                `json:"effective_on"`
	CatchUpFrom      string                `json:"catch_up_from"`
	AcknowledgePast  bool                  `json:"acknowledge_past"`
}

type ChoreActionInput struct {
	ChoreCommand
	Action        string  `json:"action"`
	DueOn         string  `json:"due_on,omitempty"`
	LocalTime     string  `json:"local_time,omitempty"`
	Timezone      string  `json:"timezone,omitempty"`
	AssigneeID    *string `json:"assignee_id,omitempty"`
	Title         *string `json:"title,omitempty"`
	Description   *string `json:"description,omitempty"`
	ActualMinutes *int    `json:"actual_minutes,omitempty"`
	ChecklistKey  string  `json:"checklist_key,omitempty"`
	Done          bool    `json:"done"`
	PauseUntil    string  `json:"pause_until,omitempty"`
}

type ChoreQuery struct {
	From            string
	Through         string
	Scope           string
	AreaID          string
	TemplateID      string
	State           string
	Search          string
	Cursor          string
	Limit           int
	IncludePaused   bool
	IncludeArchived bool
}

type choreReadScope struct {
	ctx        context.Context
	tx         pgx.Tx
	cancel     context.CancelFunc
	generation int64
	now        time.Time
	timezone   string
}

func (scope *choreReadScope) close() {
	rollback(scope.tx)
	scope.cancel()
}

func choreIdentityValid(id auth.Identity) bool {
	return value.ValidUUID(id.HouseholdID) && value.ValidUUID(id.UserID) &&
		value.ValidUUID(id.MembershipID) && value.ValidUUID(id.SessionID) && id.AuthVersion > 0 &&
		!id.MustChange && (id.Role == "admin" || id.Role == "editor" || id.Role == "reader")
}

func choreStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505", "40001", "40P01", "55P03":
			return ErrConflict
		case "23514", "23503", "23502", "22007", "22008", "22023":
			return Invalid{"Aufgabenstand, Pflichtschritte, Personen und Regel prüfen"}
		}
	}
	return err
}

func (s *Store) choreRead(ctx context.Context, id auth.Identity) (*choreReadScope, error) {
	if !choreIdentityValid(id) {
		return nil, ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, choreReadTimeout)
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		cancel()
		return nil, err
	}
	scope := &choreReadScope{ctx: ctx, cancel: cancel, tx: tx}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.sessions session JOIN core.users u ON u.id=session.user_id
 JOIN core.memberships member ON member.user_id=u.id
 WHERE session.id=$1 AND u.id=$2 AND member.household_id=$3 AND member.id=$4 AND member.role=$5
 AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND u.auth_version=$6 AND session.auth_version=u.auth_version AND session.revoked_at IS NULL
 AND session.idle_expires_at>now() AND session.absolute_expires_at>now()`, id.SessionID, id.UserID,
		id.HouseholdID, id.MembershipID, id.Role, id.AuthVersion).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT state.data_generation,transaction_timestamp(),household.timezone
 FROM core.module_states state JOIN core.households household ON household.id=state.household_id
 WHERE state.household_id=$1 AND state.module_id='chores' AND state.state='enabled'`, id.HouseholdID).
			Scan(&scope.generation, &scope.now, &scope.timezone)
	}
	if err != nil {
		scope.close()
		return nil, choreStoreError(err)
	}
	return scope, nil
}

func choreObject(ctx context.Context, tx pgx.Tx, query string, arguments ...any) (Record, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, query, arguments...).Scan(&raw); err != nil {
		return nil, choreStoreError(err)
	}
	if len(raw) > 2<<20 {
		return nil, Invalid{"Aufgabendetail ist zu umfangreich"}
	}
	return decode(raw)
}

func choreRows(ctx context.Context, tx pgx.Tx, limit int, query string, arguments ...any) ([]Record, error) {
	if limit < 1 || limit > chore.MaximumPlanItems {
		return nil, Invalid{"Auswahlumfang ist nicht unterstützt"}
	}
	rows, err := tx.Query(ctx, query, arguments...)
	if err != nil {
		return nil, choreStoreError(err)
	}
	defer rows.Close()
	result := []Record{}
	total := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		total += len(raw)
		if len(result) == limit || total > 2<<20 {
			return nil, Invalid{"Auswahl ist zu groß; Zeitraum oder Filter eingrenzen"}
		}
		var record Record
		if record, err = decode(raw); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// choreWrite checks current authority before looking up a previous receipt.
// Unique occurrence/completion facts remain the duplicate barrier if an old
// Core response receipt has already expired or was pruned.
func (s *Store) choreWrite(ctx context.Context, id auth.Identity, key, requestID, operation string,
	base ChoreCommand, payload any, personal bool, authorize func(context.Context, pgx.Tx) error,
	apply func(context.Context, pgx.Tx) (Record, error)) (Record, error) {
	if !choreIdentityValid(id) || !personal && !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(key) || base.Generation < 1 || len(operation) < 1 || len(operation) > 200 {
		return nil, Invalid{"Vorgangskennung und Aufgabengeneration prüfen"}
	}
	ctx, cancel := context.WithTimeout(ctx, choreOperationTimeout)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	var tx pgx.Tx
	var err error
	if personal {
		tx, err = audit.Begin(ctx, s.Pool)
		if err == nil {
			_, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID)
		}
		if err == nil {
			err = checkSession(ctx, tx, id)
		}
		if err == nil {
			var generation int64
			generation, err = requireModule(ctx, tx, id, "chores")
			if err == nil && generation != base.Generation {
				err = ErrConflict
			}
		}
		if err == nil {
			_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('household/chores/'||$1::text,0))", id.HouseholdID)
		}
	} else {
		tx, err = s.BeginMutation(ctx, id, "chores", base.Generation)
	}
	if err != nil {
		if tx != nil {
			rollback(tx)
		}
		return nil, choreStoreError(err)
	}
	defer rollback(tx)
	if authorize != nil {
		if err = authorize(ctx, tx); err != nil {
			return nil, choreStoreError(err)
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64<<10 {
		return nil, Invalid{"Aufgabenentscheidung ist zu umfangreich"}
	}
	hash := sha256.Sum256(encoded)
	var previousHash, previous []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, id.HouseholdID, id.UserID, operation, key).
		Scan(&previousHash, &previous)
	if err == nil {
		if !bytes.Equal(previousHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(previous)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	result, err := apply(ctx, tx)
	if err != nil {
		return nil, choreStoreError(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(raw) > 128<<10 {
		return nil, Invalid{"Aufgabenergebnis ist zu umfangreich"}
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,
 module_generation,response_status,response_payload,target_generations) VALUES($1,$2,$3,$4,$5,'chores',$6,200,$7,'{}'::jsonb)`,
		id.HouseholdID, id.UserID, operation, key, hash[:], base.Generation, raw)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, choreStoreError(err)
	}
	return result, nil
}
