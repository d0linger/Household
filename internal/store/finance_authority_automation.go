package store

import (
	"context"
	"encoding/json"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financeAutomationProjection struct {
	HouseholdID  string    `json:"household_id"`
	UserID       string    `json:"user_id"`
	MembershipID string    `json:"membership_id"`
	Role         string    `json:"role"`
	AuthVersion  int64     `json:"auth_version"`
	Generation   int64     `json:"generation"`
	StateVersion int64     `json:"state_version"`
	Timezone     string    `json:"timezone"`
	Currency     string    `json:"currency"`
	ReadAt       time.Time `json:"read_at"`
}

func decodeFinanceAutomation(raw []byte) (auth.Identity, financialScope, error) {
	var data financeAutomationProjection
	if len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &data) != nil || !value.ValidUUID(data.HouseholdID) ||
		!value.ValidUUID(data.UserID) || !value.ValidUUID(data.MembershipID) || data.AuthVersion <= 0 ||
		data.Generation <= 0 || data.StateVersion <= 0 || data.ReadAt.IsZero() {
		return auth.Identity{}, financialScope{}, ErrForbidden
	}
	identity := auth.Identity{HouseholdID: data.HouseholdID, UserID: data.UserID,
		MembershipID: data.MembershipID, Role: data.Role, AuthVersion: data.AuthVersion}
	if !identity.CanWrite() {
		return auth.Identity{}, financialScope{}, ErrForbidden
	}
	return identity, financialScope{householdID: data.HouseholdID, userID: data.UserID,
		membershipID: data.MembershipID, role: data.Role, authVersion: data.AuthVersion,
		generation: data.Generation, stateVersion: data.StateVersion, timezone: data.Timezone,
		currency: data.Currency, readAt: data.ReadAt}, nil
}

func financeAutomationScope(ctx context.Context, tx pgx.Tx, jobID, leaseToken, templateID string) (auth.Identity, financialScope, error) {
	if !value.ValidUUID(jobID) || !value.ValidUUID(leaseToken) || !value.ValidUUID(templateID) {
		return auth.Identity{}, financialScope{}, ErrForbidden
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT core.finance_automation_scope($1,$2,$3)", jobID, leaseToken, templateID).Scan(&raw)
	if err != nil {
		return auth.Identity{}, financialScope{}, financeAuthorityError(err)
	}
	return decodeFinanceAutomation(raw)
}

func financeCurrentAutomationScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (financialScope, error) {
	if id.SessionID != "" || !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) ||
		!value.ValidUUID(id.MembershipID) || id.AuthVersion <= 0 {
		return financialScope{}, ErrForbidden
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT core.finance_current_automation_scope($1,$2,$3,$4)",
		id.HouseholdID, id.UserID, id.MembershipID, id.AuthVersion).Scan(&raw)
	if err != nil {
		return financialScope{}, financeAuthorityError(err)
	}
	identity, scope, err := decodeFinanceAutomation(raw)
	if err != nil || identity.Role != id.Role || identity.UserID != id.UserID || identity.MembershipID != id.MembershipID {
		return financialScope{}, ErrForbidden
	}
	return scope, nil
}

func setFinanceAutomation(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, templateID string, enabled bool) error {
	if !value.ValidUUID(templateID) || id.SessionID == "" {
		return ErrForbidden
	}
	intent := "edit"
	if enabled {
		intent = "manage"
	}
	if _, err := financeResourceAccess(ctx, tx, id, scope, "recurring_templates", templateID, intent); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT core.finance_set_automation($1,$2,$3,$4,$5,$6,$7,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, templateID, enabled)
	return financeAuthorityError(err)
}

func financeAutomationInfo(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, templateID string) (Record, error) {
	if _, err := financeResourceAccess(ctx, tx, id, scope, "recurring_templates", templateID, "view"); err != nil {
		return nil, err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT core.finance_automation_info($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, templateID).Scan(&raw)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	return decode(raw)
}
