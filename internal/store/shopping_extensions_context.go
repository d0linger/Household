package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func shoppingRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func shoppingScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (ShoppingExtensionContext, error) {
	var out ShoppingExtensionContext
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.SessionID) ||
		!value.ValidUUID(id.MembershipID) || id.AuthVersion <= 0 {
		return out, ErrForbidden
	}
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT ms.data_generation,ms.state='enabled',coalesce(p.offline_storage_enabled,false),clock_timestamp()
 FROM core.module_states ms JOIN core.memberships m ON m.household_id=ms.household_id AND m.id=$2 AND m.user_id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.auth_version=$5 AND u.disabled_at IS NULL AND NOT u.must_change_password
 JOIN core.sessions s ON s.id=$6 AND s.user_id=u.id AND s.auth_version=u.auth_version AND s.revoked_at IS NULL
 AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp()
 LEFT JOIN core.user_preferences p ON p.household_id=ms.household_id AND p.user_id=u.id
 WHERE ms.household_id=$1 AND ms.module_id='shopping'`, id.HouseholdID, id.MembershipID, id.UserID, id.Role, id.AuthVersion, id.SessionID).
		Scan(&out.Generation, &enabled, &out.OfflineAllowed, &out.ServerTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrForbidden
	}
	if err != nil {
		return out, err
	}
	if !enabled {
		return out, ErrNotFound
	}
	if out.Generation <= 0 || out.Generation > shoppingSafeInteger {
		return out, Invalid{"Einkaufsgeneration wird nicht unterstützt"}
	}
	out.UserID, out.HouseholdID, out.Role, out.CanWrite = id.UserID, id.HouseholdID, id.Role, id.CanWrite()
	out.ServerTime = out.ServerTime.UTC()
	for _, item := range []struct {
		label  string
		target *string
	}{{"shopping-session-v1", &out.SessionScope}, {"shopping-authority-v1", &out.AuthScope}} {
		raw, _ := json.Marshal([]any{item.label, id.HouseholdID, id.UserID, id.SessionID, id.MembershipID, id.Role, id.AuthVersion, out.Generation})
		h := sha256.Sum256(raw)
		*item.target = hex.EncodeToString(h[:])
	}
	return out, nil
}

func (s *Store) ShoppingExtensionsContext(ctx context.Context, id auth.Identity) (ShoppingExtensionContext, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ShoppingExtensionContext{}, err
	}
	defer shoppingRollback(tx)
	out, err := shoppingScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *Store) shoppingPreferenceTransaction(ctx context.Context, id auth.Identity) (pgx.Tx, ShoppingExtensionContext, error) {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return nil, ShoppingExtensionContext{}, err
	}
	fail := func(e error) (pgx.Tx, ShoppingExtensionContext, error) {
		shoppingRollback(tx)
		return nil, ShoppingExtensionContext{}, e
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID); err != nil {
		return fail(err)
	}
	if err = checkSession(ctx, tx, id); err != nil {
		return fail(err)
	}
	var enabled bool
	if err = tx.QueryRow(ctx, "SELECT state='enabled' FROM core.module_states WHERE household_id=$1 AND module_id='shopping' FOR UPDATE NOWAIT", id.HouseholdID).Scan(&enabled); err != nil {
		return fail(shoppingContention(err))
	}
	if !enabled {
		return fail(ErrNotFound)
	}
	scope, err := shoppingScope(ctx, tx, id)
	if err != nil {
		return fail(err)
	}
	return tx, scope, nil
}

func shoppingDecodeClosed(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return Invalid{"Gespeicherte Einkaufseinstellungen sind ungültig"}
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Invalid{"Gespeicherte Einkaufseinstellungen sind ungültig"}
	}
	return nil
}

func shoppingRevision(raw string) (uint64, error) {
	if raw == "" || len(raw) > 20 || len(raw) > 1 && raw[0] == '0' {
		return 0, Invalid{"Revision erneut laden"}
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, Invalid{"Revision erneut laden"}
		}
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, Invalid{"Revision erneut laden"}
	}
	return n, nil
}

func shoppingCanonicalUUID(v string) bool { return value.ValidUUID(v) && v == strings.ToLower(v) }

func shoppingValidateSettings(in shoppingNotifySettings, generation int64) error {
	if in.Schema != 1 || in.Generation != generation || in.Users == nil {
		return Invalid{"Gespeicherte Einkaufseinstellungen erneut prüfen"}
	}
	count := 0
	for uid, user := range in.Users {
		rev, err := shoppingRevision(user.Revision)
		if !shoppingCanonicalUUID(uid) || err != nil || rev == 0 || user.Watches == nil || len(user.Watches) > 20 {
			return Invalid{"Gespeicherte Einkaufseinstellungen erneut prüfen"}
		}
		for lid, w := range user.Watches {
			count++
			if !shoppingCanonicalUUID(lid) || !w.Enabled || w.EnabledSince.Year() < 2000 || w.EnabledSince.Year() > 9999 ||
				w.PushEnabled != (w.PushEnabledSince != nil) || w.PushEnabledSince != nil && (w.PushEnabledSince.Before(w.EnabledSince) || w.PushEnabledSince.Year() > 9999) ||
				w.MutedUntil != nil && (w.MutedUntil.Year() < 2000 || w.MutedUntil.Year() > 9999) {
				return Invalid{"Gespeicherte Einkaufseinstellungen erneut prüfen"}
			}
		}
	}
	if count > 256 {
		return Invalid{"Zu viele Listenabonnements"}
	}
	return nil
}

func shoppingReadSettings(ctx context.Context, tx pgx.Tx, household string, generation int64) (shoppingNotifySettings, error) {
	empty := shoppingNotifySettings{Schema: 1, Generation: generation, Users: map[string]shoppingNotifyUser{}}
	var raw []byte
	var valid, bounded bool
	err := tx.QueryRow(ctx, `SELECT jsonb_typeof(settings)='object',coalesce(octet_length((settings->($2::text))::text),0)<=$3,
 CASE WHEN coalesce(octet_length((settings->($2::text))::text),0)<=$3 THEN settings->($2::text) END
 FROM core.module_states WHERE household_id=$1 AND module_id='shopping'`, household, shoppingNotifyNamespace, shoppingNotifyBytes).Scan(&valid, &bounded, &raw)
	if err != nil {
		return empty, err
	}
	if !valid || !bounded {
		return empty, Invalid{"Einkaufseinstellungen überschreiten sichere Grenzen"}
	}
	if raw == nil {
		return empty, nil
	}
	var out shoppingNotifySettings
	if err = shoppingDecodeClosed(raw, &out); err != nil {
		return empty, err
	}
	if err = shoppingValidateSettings(out, generation); err != nil {
		return empty, err
	}
	return out, nil
}

func shoppingReadList(ctx context.Context, tx pgx.Tx, household, list string) (ShoppingPreferenceList, error) {
	var out ShoppingPreferenceList
	if !value.ValidUUID(list) {
		return out, ErrNotFound
	}
	err := tx.QueryRow(ctx, "SELECT id,name,archived_at IS NOT NULL,is_template FROM shopping.lists WHERE household_id=$1 AND id=$2", household, list).Scan(&out.ID, &out.Name, &out.Archived, &out.IsTemplate)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func shoppingWriteSettings(ctx context.Context, tx pgx.Tx, household string, settings shoppingNotifySettings) error {
	if err := shoppingValidateSettings(settings, settings.Generation); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if len(raw) > shoppingNotifyBytes {
		return Invalid{"Zu viele persönliche Einkaufsabonnements"}
	}
	// jsonb's canonical text includes whitespace not present in Go's encoder.
	// Enforce the same stored-byte boundary used by every subsequent read.
	var bounded bool
	if err = tx.QueryRow(ctx, "SELECT octet_length($1::jsonb::text)<=$2", raw, shoppingNotifyBytes).Scan(&bounded); err != nil {
		return err
	}
	if !bounded {
		return Invalid{"Zu viele persönliche Einkaufsabonnements"}
	}
	tag, err := tx.Exec(ctx, `UPDATE core.module_states SET settings=jsonb_set(settings,ARRAY[$2]::text[],$3::jsonb,true)
 WHERE household_id=$1 AND module_id='shopping' AND state='enabled' AND data_generation=$4`, household, shoppingNotifyNamespace, raw, settings.Generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
