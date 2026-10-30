package webpush

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type storedSubscription struct {
	id, household, user, keyID string
	hash, cipher               []byte
	disabled                   *time.Time
	created                    time.Time
}

func (s *Service) open(row storedSubscription) (envelope, error) {
	var data envelope
	if row.keyID != s.encryptionID || len(row.cipher) < 32 || len(row.cipher) > maxCipher {
		return data, store.ErrConflict
	}
	raw, err := s.auth.Open(row.cipher)
	if err != nil || len(raw) > maxCipher || strictJSON(raw, &data) != nil {
		return data, store.ErrConflict
	}
	validIDs := value.ValidUUID(data.ID) && value.ValidUUID(data.HouseholdID) && value.ValidUUID(data.UserID) && value.ValidUUID(data.DeviceID) && value.ValidUUID(data.SessionID)
	if data.Schema != 1 || !validIDs || data.ID != row.id || data.HouseholdID != row.household || data.UserID != row.user || data.AuthVersion < 1 {
		return envelope{}, store.ErrConflict
	}
	if data.Role != "admin" && data.Role != "editor" && data.Role != "reader" {
		return envelope{}, store.ErrConflict
	}
	identity := auth.Identity{UserID: data.UserID, HouseholdID: data.HouseholdID, SessionID: data.SessionID, AuthVersion: data.AuthVersion, Role: data.Role}
	scope := personal(identity)
	if data.AuthScope != scope.AuthScope || data.SessionScope != scope.SessionScope || !hexDigest(data.VAPIDKeyID) {
		return envelope{}, store.ErrConflict
	}
	validTimes := !data.RegisteredAt.IsZero() && data.ConsentAt.Equal(data.RegisteredAt) && data.RegisteredAt.Equal(row.created) && data.ExpiresAt.After(data.ConsentAt) && data.ExpiresAt.Sub(data.ConsentAt) <= 30*24*time.Hour
	if !validTimes || validateReceiver(data.Subscription) != nil {
		return envelope{}, store.ErrConflict
	}
	hash := sha256.Sum256([]byte(data.Subscription.Endpoint))
	if !equalBytes(row.hash, hash[:]) {
		return envelope{}, store.ErrConflict
	}
	return data, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var difference byte
	for i := range a {
		difference |= a[i] ^ b[i]
	}
	return difference == 0
}

func currentEnvelope(ctx context.Context, tx pgx.Tx, data envelope) (bool, error) {
	var current bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.sessions s JOIN core.users u ON u.id=s.user_id
 JOIN core.memberships m ON m.user_id=u.id WHERE s.id=$1 AND s.user_id=$2 AND m.household_id=$3
 AND s.revoked_at IS NULL AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp()
 AND s.auth_version=u.auth_version AND u.auth_version=$4 AND NOT u.must_change_password
 AND u.disabled_at IS NULL AND m.inactive_at IS NULL AND m.role=$5)`, data.SessionID, data.UserID, data.HouseholdID, data.AuthVersion, data.Role).Scan(&current)
	return current, err
}

func (s *Service) ownerRows(ctx context.Context, tx pgx.Tx, id auth.Identity) ([]storedSubscription, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,household_id::text,user_id::text,endpoint_hash,encrypted_subscription,
 encryption_key_id,disabled_at,created_at FROM core.push_subscriptions WHERE household_id=$1 AND user_id=$2
 AND octet_length(encrypted_subscription)<=$3 ORDER BY created_at DESC,id LIMIT 129 FOR UPDATE`, id.HouseholdID, id.UserID, maxCipher)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []storedSubscription{}
	for rows.Next() {
		var row storedSubscription
		if err = rows.Scan(&row.id, &row.household, &row.user, &row.hash, &row.cipher, &row.keyID, &row.disabled, &row.created); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(result) > maxHistory {
		return nil, ErrLimited
	}
	var oversized bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.push_subscriptions WHERE household_id=$1 AND user_id=$2 AND octet_length(encrypted_subscription)>$3)`, id.HouseholdID, id.UserID, maxCipher).Scan(&oversized); err != nil {
		return nil, err
	}
	if oversized {
		return nil, store.ErrConflict
	}
	return result, nil
}

func (s *Service) Subscriptions(ctx context.Context, id auth.Identity) (Subscriptions, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.begin(ctx, id)
	if err != nil {
		return Subscriptions{}, err
	}
	defer rollback(tx)
	rows, err := s.ownerRows(ctx, tx, id)
	if err != nil {
		return Subscriptions{}, err
	}
	result := Subscriptions{Subscriptions: []SubscriptionView{}}
	for _, row := range rows {
		data, err := s.open(row)
		if err != nil {
			return Subscriptions{}, err
		}
		state := "registered"
		switch {
		case row.disabled != nil:
			state = "disabled"
		case !data.ExpiresAt.After(time.Now()):
			state = "expired"
		case data.VAPIDKeyID != s.vapidID:
			state = "renewal_required"
		default:
			current, err := currentEnvelope(ctx, tx, data)
			if err != nil {
				return Subscriptions{}, err
			}
			if !current {
				state = "expired"
			}
		}
		result.Subscriptions = append(result.Subscriptions, SubscriptionView{ID: row.id, DeviceID: data.DeviceID, State: state, VAPIDKeyID: data.VAPIDKeyID, RegisteredAt: data.RegisteredAt})
	}
	if err = tx.Commit(ctx); err != nil {
		return Subscriptions{}, err
	}
	return result, nil
}

func (s *Service) managementBudget(ctx context.Context, id auth.Identity, scope string, limit int) error {
	tx, err := s.begin(ctx, id)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = counter(ctx, tx, scope, digest(id.HouseholdID+"/"+id.UserID), limit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Register(ctx context.Context, id auth.Identity, in RegisterInput) (SubscriptionView, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s == nil || s.state != "ready" {
		return SubscriptionView{}, ErrUnavailable
	}
	if !value.ValidUUID(in.DeviceID) || strings.ToLower(in.DeviceID) != in.DeviceID || in.VAPIDKeyID != s.vapidID {
		return SubscriptionView{}, store.Invalid{Message: "Push-Gerät oder Anwendungsschlüssel erneut prüfen"}
	}
	if err := validateReceiver(in.Subscription); err != nil {
		return SubscriptionView{}, err
	}
	canonical, err := canonicalEndpoint(in.Subscription.Endpoint)
	if err != nil {
		return SubscriptionView{}, store.Invalid{Message: "Push-Ziel ist nicht zulässig"}
	}
	in.Subscription.Endpoint = canonical
	if err := s.managementBudget(ctx, id, "webpush-register-v1", 12); err != nil {
		return SubscriptionView{}, err
	}
	tx, err := s.begin(ctx, id)
	if err != nil {
		return SubscriptionView{}, err
	}
	defer rollback(tx)
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", "webpush-owner-v1/"+id.HouseholdID+"/"+id.UserID).Scan(&locked); err != nil {
		return SubscriptionView{}, err
	}
	if !locked {
		return SubscriptionView{}, store.ErrConflict
	}
	hash := sha256.Sum256([]byte(in.Subscription.Endpoint))
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", "webpush-endpoint-v1/"+digest(in.Subscription.Endpoint)).Scan(&locked); err != nil {
		return SubscriptionView{}, err
	}
	if !locked {
		return SubscriptionView{}, store.ErrConflict
	}
	var existingID, existingHousehold, existingUser string
	err = tx.QueryRow(ctx, `SELECT id::text,household_id::text,user_id::text FROM core.push_subscriptions WHERE endpoint_hash=$1 FOR UPDATE`, hash[:]).Scan(&existingID, &existingHousehold, &existingUser)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SubscriptionView{}, err
	}
	if err == nil && (existingHousehold != id.HouseholdID || existingUser != id.UserID) {
		return SubscriptionView{}, store.ErrConflict
	}
	rows, err := s.ownerRows(ctx, tx, id)
	if err != nil {
		return SubscriptionView{}, err
	}
	var chosen *storedSubscription
	active := 0
	for i := range rows {
		row := &rows[i]
		data, err := s.open(*row)
		if err != nil {
			return SubscriptionView{}, err
		}
		if data.DeviceID == in.DeviceID {
			if chosen != nil {
				return SubscriptionView{}, store.ErrConflict
			}
			chosen = row
		}
		current, err := currentEnvelope(ctx, tx, data)
		if err != nil {
			return SubscriptionView{}, err
		}
		if row.disabled == nil && (!current || !data.ExpiresAt.After(time.Now()) || data.VAPIDKeyID != s.vapidID) {
			if _, err = tx.Exec(ctx, "UPDATE core.push_subscriptions SET disabled_at=clock_timestamp() WHERE id=$1", row.id); err != nil {
				return SubscriptionView{}, err
			}
			retired := time.Now()
			row.disabled = &retired
		}
		if row.disabled == nil && data.ExpiresAt.After(time.Now()) && current && data.VAPIDKeyID == s.vapidID && data.DeviceID != in.DeviceID {
			active++
		}
	}
	if existingID != "" && (chosen == nil || chosen.id != existingID) {
		return SubscriptionView{}, store.ErrConflict
	}
	if active >= DeviceLimit {
		return SubscriptionView{}, store.Invalid{Message: "Höchstens fünf aktive Push-Geräte. Zuerst ein eigenes Gerät trennen"}
	}
	rid := value.UUID()
	if chosen != nil {
		rid = chosen.id
	}
	var now, absolute time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp(),absolute_expires_at FROM core.sessions WHERE id=$1", id.SessionID).Scan(&now, &absolute); err != nil {
		return SubscriptionView{}, err
	}
	expires := now.Add(30 * 24 * time.Hour)
	if absolute.Before(expires) {
		expires = absolute
	}
	if !expires.After(now) {
		return SubscriptionView{}, store.ErrForbidden
	}
	scope := personal(id)
	data := envelope{Schema: 1, ID: rid, HouseholdID: id.HouseholdID, UserID: id.UserID, DeviceID: in.DeviceID, SessionID: id.SessionID, AuthVersion: id.AuthVersion, Role: id.Role, SessionScope: scope.SessionScope, AuthScope: scope.AuthScope, RegisteredAt: now, ConsentAt: now, ExpiresAt: expires, VAPIDKeyID: s.vapidID, Subscription: in.Subscription}
	raw, err := json.Marshal(data)
	if err != nil {
		return SubscriptionView{}, errors.New("encode push connection failed")
	}
	cipher := s.auth.Seal(raw)
	if len(cipher) > maxCipher {
		return SubscriptionView{}, store.Invalid{Message: "Push-Verbindung ist zu groß"}
	}
	if chosen == nil {
		_, err = tx.Exec(ctx, `INSERT INTO core.push_subscriptions(id,household_id,user_id,endpoint_hash,encrypted_subscription,encryption_key_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, rid, id.HouseholdID, id.UserID, hash[:], cipher, s.encryptionID, now)
	} else {
		_, err = tx.Exec(ctx, `UPDATE core.push_subscriptions SET endpoint_hash=$4,encrypted_subscription=$5,encryption_key_id=$6,disabled_at=NULL,created_at=$7 WHERE id=$1 AND household_id=$2 AND user_id=$3`, rid, id.HouseholdID, id.UserID, hash[:], cipher, s.encryptionID, now)
	}
	if err != nil {
		return SubscriptionView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SubscriptionView{}, err
	}
	return SubscriptionView{ID: rid, DeviceID: in.DeviceID, State: "registered", VAPIDKeyID: s.vapidID, RegisteredAt: now}, nil
}

func (s *Service) Disable(ctx context.Context, id auth.Identity, rid string) (Disabled, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !value.ValidUUID(rid) {
		return Disabled{}, store.ErrNotFound
	}
	if err := s.managementBudget(ctx, id, "webpush-disable-v1", 60); err != nil {
		return Disabled{}, err
	}
	tx, err := s.begin(ctx, id)
	if err != nil {
		return Disabled{}, err
	}
	defer rollback(tx)
	tag, err := tx.Exec(ctx, `UPDATE core.push_subscriptions SET disabled_at=coalesce(disabled_at,clock_timestamp()) WHERE id=$1 AND household_id=$2 AND user_id=$3`, rid, id.HouseholdID, id.UserID)
	if err != nil {
		return Disabled{}, err
	}
	if tag.RowsAffected() != 1 {
		return Disabled{}, store.ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return Disabled{}, err
	}
	return Disabled{State: "disabled"}, nil
}
