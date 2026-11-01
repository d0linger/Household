package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type EnergyCommand struct {
	Generation        int64                      `json:"generation"`
	Version           int64                      `json:"version"`
	Action            string                     `json:"action"`
	ID                string                     `json:"id"`
	Data              map[string]json.RawMessage `json:"data"`
	TargetGenerations map[string]int64           `json:"target_generations,omitempty"`
}

type EnergyQuery struct {
	RegisterID string
	From       string
	Through    string
	Cursor     string
	Grain      string
	Search     string
}

type energyReadScope struct {
	ctx        context.Context
	tx         pgx.Tx
	cancel     context.CancelFunc
	generation int64
	now        time.Time
	timezone   string
	household  string
}

func (scope *energyReadScope) close() {
	rollback(scope.tx)
	scope.cancel()
}

func (s *Store) EnergyContext(ctx context.Context, id auth.Identity) (Record, error) {
	scope, err := s.energyRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	states, err := energyRows(scope.ctx, scope.tx, 10, `SELECT jsonb_build_object('id',module_id,'generation',data_generation,'state',state)
 FROM core.module_states WHERE household_id=$1 ORDER BY module_id LIMIT 11`, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	return Record{"generation": scope.generation, "timezone": scope.timezone, "now": scope.now,
		"can_write": id.CanWrite(), "can_configure": id.Role == "admin", "module_states": states}, nil
}

func energyIdentityValid(id auth.Identity) bool {
	return value.ValidUUID(id.HouseholdID) && value.ValidUUID(id.UserID) &&
		value.ValidUUID(id.MembershipID) && value.ValidUUID(id.SessionID) && id.AuthVersion > 0 &&
		!id.MustChange && slices.Contains([]string{"admin", "editor", "reader"}, id.Role)
}

func energyError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if problem, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch problem.Code {
		case "23505", "40001", "40P01", "55P03":
			return ErrConflict
		case "23514", "23503", "23502", "22007", "22008", "22023":
			return Invalid{"Zähler, Messgröße, Einheit, Zeitpunkt und benachbarte Messwerte prüfen"}
		case "42501":
			return ErrForbidden
		}
	}
	return err
}

func (s *Store) energyRead(ctx context.Context, id auth.Identity) (*energyReadScope, error) {
	if !energyIdentityValid(id) {
		return nil, ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		cancel()
		return nil, err
	}
	scope := &energyReadScope{ctx: ctx, tx: tx, cancel: cancel, household: id.HouseholdID}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.sessions session JOIN core.users u ON u.id=session.user_id
 JOIN core.memberships member ON member.user_id=u.id WHERE session.id=$1 AND u.id=$2 AND member.household_id=$3
 AND member.id=$4 AND member.role=$5 AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND u.auth_version=$6 AND session.auth_version=u.auth_version AND session.revoked_at IS NULL
 AND session.idle_expires_at>now() AND session.absolute_expires_at>now()`,
		id.SessionID, id.UserID, id.HouseholdID, id.MembershipID, id.Role, id.AuthVersion).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT state.data_generation,transaction_timestamp(),household.timezone
 FROM core.module_states state JOIN core.households household ON household.id=state.household_id
 WHERE state.household_id=$1 AND state.module_id='meters' AND state.state='enabled'`, id.HouseholdID).
			Scan(&scope.generation, &scope.now, &scope.timezone)
	}
	if err != nil {
		scope.close()
		return nil, energyError(err)
	}
	return scope, nil
}

func energyObject(ctx context.Context, tx pgx.Tx, query string, args ...any) (Record, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		return nil, energyError(err)
	}
	if len(raw) > 128<<10 {
		return nil, Invalid{"Zählerdetail ist zu groß; Zeitraum oder Auswahl begrenzen"}
	}
	return decode(raw)
}

func energyRows(ctx context.Context, tx pgx.Tx, limit int, query string, args ...any) ([]Record, error) {
	if limit < 1 || limit > 2500 {
		return nil, Invalid{"Zählerauswahl ist zu groß"}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, energyError(err)
	}
	defer rows.Close()
	out := []Record{}
	bytesRead := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		bytesRead += len(raw)
		if len(out) == limit || bytesRead > 2<<20 {
			return nil, Invalid{"Zählerauswahl ist zu groß; Zeitraum oder Aggregation eingrenzen"}
		}
		row, err := decode(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) EnergyWrite(ctx context.Context, id auth.Identity, key, requestID string, in EnergyCommand) (Record, error) {
	if !energyIdentityValid(id) || !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(key) || in.Generation < 1 || in.Version < 0 || in.ID != "" && !value.ValidUUID(in.ID) || len(in.TargetGenerations) > 1 {
		return nil, Invalid{"Vorgang, Generation und Versionsstand prüfen"}
	}
	data, err := energyNormalize(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "meters", in.Generation)
	if err != nil {
		return nil, energyError(err)
	}
	defer rollback(tx)
	if err = s.energyAuthorizeCommand(ctx, tx, id, in); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 1<<20 {
		return nil, Invalid{"Zählervorgang ist zu groß"}
	}
	digest := sha256.Sum256(raw)
	var oldHash, receipt []byte
	var oldOperation string
	var oldGeneration int64
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload,operation,module_generation FROM meters.command_receipts
 WHERE household_id=$1 AND user_id=$2 AND command_key=$3`, id.HouseholdID, id.UserID, key).
		Scan(&oldHash, &receipt, &oldOperation, &oldGeneration)
	if err == nil {
		if !bytes.Equal(oldHash, digest[:]) || oldOperation != in.Action || oldGeneration != in.Generation {
			return nil, ErrConflict
		}
		return decode(receipt)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	out, err := s.energyApply(ctx, tx, id, key, requestID, in, data)
	if err != nil {
		return nil, energyError(err)
	}
	receipt, err = json.Marshal(out)
	if err != nil || len(receipt) > 128<<10 {
		return nil, Invalid{"Zählerbeleg ist zu groß"}
	}
	_, err = tx.Exec(ctx, `INSERT INTO meters.command_receipts(household_id,user_id,command_key,operation,module_generation,request_hash,response_payload)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, id.HouseholdID, id.UserID, key, in.Action, in.Generation, digest[:], receipt)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return out, energyError(err)
}

func energyText(raw json.RawMessage, maximum int) (string, error) {
	var text string
	if len(raw) > maximum*6+2 || json.Unmarshal(raw, &text) != nil || !utf8.ValidString(text) || utf8.RuneCountInString(text) > maximum {
		return "", Invalid{"Textfeld des Zählervorgangs prüfen"}
	}
	return strings.TrimSpace(text), nil
}
