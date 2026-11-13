// Package securityevent records personal security changes in their mutation's
// transaction. It never contacts a push provider or carries private details.
package securityevent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const JobKind = "security_notification_source_v1"

func ValidKind(kind string) bool {
	switch kind {
	case "password_changed", "totp_enabled", "totp_disabled", "recovery_codes_regenerated", "recovery_code_used",
		"passkey_added", "passkey_removed", "sessions_revoked", "account_changed", "device_authorized":
		return true
	default:
		return false
	}
}

// Record keeps the inbox and durable delivery work atomic with the change. The
// caller supplies the affected account, which can differ from the audit actor.
func Record(ctx context.Context, tx pgx.Tx, household, user, kind string) error {
	if !ValidKind(kind) || !value.ValidUUID(household) || !value.ValidUUID(user) ||
		household == "00000000-0000-0000-0000-000000000000" || user == "00000000-0000-0000-0000-000000000000" ||
		strings.ToLower(household+user) != household+user {
		return errors.New("invalid personal security event")
	}
	id := value.UUID()
	_, err := tx.Exec(ctx, `INSERT INTO core.notifications(id,household_id,user_id,kind,title,target_path,dedupe_key)
 VALUES($1,$2,$3,$4,'Sicherheitsereignis','/settings/security',$5)`, id, household, user, "security_"+kind, "security-event/"+id)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		EventID string `json:"event_id"`
	}{EventID: id})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,kind,payload,dedupe_key)
 VALUES($1,$2,$3,$4)`, household, JobKind, payload, "security-event-job/"+id)
	return err
}

// RecordAccount resolves the account's sole household for authentication paths
// where a session does not exist yet. It never selects an arbitrary membership.
func RecordAccount(ctx context.Context, tx pgx.Tx, user, kind string) error {
	rows, err := tx.Query(ctx, `SELECT household_id::text FROM core.memberships WHERE user_id=$1 ORDER BY household_id LIMIT 2`, user)
	if err != nil {
		return err
	}
	var households []string
	for rows.Next() {
		var household string
		if err = rows.Scan(&household); err != nil {
			break
		}
		households = append(households, household)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(households) != 1 {
		return errors.New("personal security event requires one account household")
	}
	return Record(ctx, tx, households[0], user, kind)
}
