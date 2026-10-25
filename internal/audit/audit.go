// Package audit binds trusted application identity to database transactions.
// PostgreSQL triggers record committed row changes using a separate, owner-managed
// privacy policy; this package never serializes domain rows or credentials.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey struct{}

type actorContext struct {
	userID, householdID, requestID, kind string
}

func fromContext(ctx context.Context) actorContext {
	actor, _ := ctx.Value(contextKey{}).(actorContext)
	if actor.kind == "" {
		actor.kind = "system"
	}
	return actor
}

// WithActor records an already authenticated user. It does not authorize a write.
func WithActor(ctx context.Context, userID, householdID string) context.Context {
	actor := fromContext(ctx)
	actor.userID, actor.householdID, actor.kind = userID, householdID, "user"
	return context.WithValue(ctx, contextKey{}, actor)
}

// WithRequest carries the server-generated request UUID across transactions.
func WithRequest(ctx context.Context, requestID string) context.Context {
	actor := fromContext(ctx)
	actor.requestID = requestID
	return context.WithValue(ctx, contextKey{}, actor)
}

// WithSystem clears any inherited user identity for independently executed work.
// Supported kinds are system, worker, operator, and bootstrap.
func WithSystem(ctx context.Context, kind string) context.Context {
	actor := fromContext(ctx)
	actor.userID, actor.householdID, actor.kind = "", "", kind
	return context.WithValue(ctx, contextKey{}, actor)
}

// WithHousehold scopes system or operator work without claiming a user actor.
func WithHousehold(ctx context.Context, householdID string) context.Context {
	actor := fromContext(ctx)
	actor.householdID = householdID
	return context.WithValue(ctx, contextKey{}, actor)
}

func validated(ctx context.Context) (actorContext, error) {
	actor := fromContext(ctx)
	switch actor.kind {
	case "user":
		if !value.ValidUUID(actor.userID) || !value.ValidUUID(actor.householdID) {
			return actor, errors.New("audit user requires valid user and household IDs")
		}
	case "system", "worker", "operator", "bootstrap":
		if actor.userID != "" {
			return actor, errors.New("audit system context cannot impersonate a user")
		}
		if actor.householdID != "" && !value.ValidUUID(actor.householdID) {
			return actor, errors.New("invalid audit household ID")
		}
	default:
		return actor, errors.New("unsupported audit actor kind")
	}
	if actor.requestID == "" {
		actor.requestID = value.UUID()
	} else if !value.ValidUUID(actor.requestID) {
		return actor, errors.New("invalid audit request ID")
	}
	return actor, nil
}

// Bind sets only transaction-local PostgreSQL settings, avoiding pool leakage.
// Call it before writing, and again if identity becomes known during login.
func Bind(ctx context.Context, tx pgx.Tx) error {
	actor, err := validated(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT set_config('household.audit_user',$1,true),
		set_config('household.audit_household',$2,true),
		set_config('household.audit_request',$3,true),
		set_config('household.audit_kind',$4,true)`, actor.userID, actor.householdID, actor.requestID, actor.kind)
	if err != nil {
		return fmt.Errorf("bind audit transaction: %w", err)
	}
	return nil
}

// Begin starts a transaction with the request identity already bound.
func Begin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	if _, err := validated(ctx); err != nil {
		return nil, err
	}
	// Mutation guards deliberately re-read aggregates after parent locks.
	// Do not inherit an operator's repeatable-read connection default here.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	if err = Bind(ctx, tx); err != nil {
		rollback(tx)
		return nil, err
	}
	return tx, nil
}

// Exec commits a single statement and its triggered audit records atomically.
func Exec(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) (pgconn.CommandTag, error) {
	tx, err := Begin(ctx, pool)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer rollback(tx)
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return tag, err
	}
	return tag, tx.Commit(ctx)
}

// Event records a semantic operation alongside automatic row diffs. The database
// validates action names and retains only explicitly permitted detail fields.
func Event(ctx context.Context, tx pgx.Tx, module, entity, resource, action string, details map[string]any) error {
	if resource != "" && !value.ValidUUID(resource) {
		return errors.New("invalid audit resource ID")
	}
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	if len(raw) > 4096 {
		return errors.New("audit metadata exceeds limit")
	}
	_, err = tx.Exec(ctx, `SELECT core.record_audit_event($1,$2,nullif($3,'')::uuid,$4,$5::jsonb)`, module, entity, resource, action, raw)
	return err
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
