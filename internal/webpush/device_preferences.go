package webpush

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/notification"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

type DevicePreference struct {
	Category     string     `json:"category"`
	Enabled      bool       `json:"enabled"`
	Paused       bool       `json:"paused"`
	Name         string     `json:"name"`
	Version      int64      `json:"version"`
	EnabledSince *time.Time `json:"enabled_since"`
}

type DeliveryView struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	SafeCode   string     `json:"safe_code"`
	AcceptedAt *time.Time `json:"accepted_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type DeviceSettings struct {
	ID           string             `json:"id"`
	DeviceID     string             `json:"device_id"`
	State        string             `json:"state"`
	RegisteredAt time.Time          `json:"registered_at"`
	ExpiresAt    time.Time          `json:"expires_at"`
	Name         string             `json:"name"`
	Categories   []DevicePreference `json:"categories"`
	LastTest     *DeliveryView      `json:"last_test"`
}

func loadSubscription(ctx context.Context, tx pgx.Tx, household, user, subscription string, lock bool) (storedSubscription, error) {
	var row storedSubscription
	query := `SELECT id::text,household_id::text,user_id::text,endpoint_hash,encrypted_subscription,encryption_key_id,encryption_format,disabled_at,created_at
 FROM core.push_subscriptions WHERE id=$1 AND household_id=$2 AND user_id=$3 AND octet_length(encrypted_subscription)<=$4`
	if lock {
		query += " FOR UPDATE"
	} else {
		query += " FOR SHARE"
	}
	err := tx.QueryRow(ctx, query, subscription, household, user, maxCipher).Scan(&row.id, &row.household, &row.user, &row.hash, &row.cipher, &row.keyID, &row.format, &row.disabled, &row.created)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, store.ErrNotFound
	}
	return row, err
}

func (s *Service) DevicePreferences(ctx context.Context, id auth.Identity) ([]DeviceSettings, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.begin(ctx, id)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	stored, err := s.ownerRows(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT subscription_id::text,category,enabled,paused,name,version,enabled_since
 FROM core.notification_device_preferences WHERE household_id=$1 AND user_id=$2 ORDER BY subscription_id,category LIMIT 1664`, id.HouseholdID, id.UserID)
	if err != nil {
		return nil, err
	}
	prefs := map[string]map[string]DevicePreference{}
	for rows.Next() {
		var subscription string
		var p DevicePreference
		if err = rows.Scan(&subscription, &p.Category, &p.Enabled, &p.Paused, &p.Name, &p.Version, &p.EnabledSince); err != nil {
			rows.Close()
			return nil, err
		}
		if prefs[subscription] == nil {
			prefs[subscription] = map[string]DevicePreference{}
		}
		prefs[subscription][p.Category] = p
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT DISTINCT ON(d.subscription_id) d.subscription_id::text,d.id::text,d.state,d.safe_code,d.accepted_at,d.updated_at
 FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE d.household_id=$1 AND d.user_id=$2 AND i.category='test'
 ORDER BY d.subscription_id,d.created_at DESC,d.id DESC LIMIT 128`, id.HouseholdID, id.UserID)
	if err != nil {
		return nil, err
	}
	tests := map[string]*DeliveryView{}
	for rows.Next() {
		var subscription string
		v := new(DeliveryView)
		if err = rows.Scan(&subscription, &v.ID, &v.State, &v.SafeCode, &v.AcceptedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		tests[subscription] = v
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := make([]DeviceSettings, 0, len(stored))
	for _, row := range stored {
		data, e := s.open(row)
		if e != nil {
			return nil, e
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
			current, e := currentEnvelope(ctx, tx, data)
			if e != nil {
				return nil, e
			}
			if !current {
				state = "expired"
			}
		}
		view := DeviceSettings{ID: row.id, DeviceID: data.DeviceID, State: state, RegisteredAt: data.RegisteredAt, ExpiresAt: data.ExpiresAt, Categories: []DevicePreference{}, LastTest: tests[row.id]}
		for _, category := range notification.Categories() {
			p, exists := prefs[row.id][category.Category]
			if !exists {
				p.Category = category.Category
			}
			view.Categories = append(view.Categories, p)
			if p.Name != "" {
				view.Name = p.Name
			}
		}
		result = append(result, view)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func validDevicePreference(p DevicePreference) bool {
	_, ok := notification.Lookup(p.Category)
	return ok && p.Version >= 0 && p.Version < notification.MaxRevision && utf8.ValidString(p.Name) && utf8.RuneCountInString(p.Name) <= 80 && strings.IndexFunc(p.Name, unicode.IsControl) < 0
}

func (s *Service) SaveDevicePreference(ctx context.Context, id auth.Identity, subscription string, p DevicePreference) (DevicePreference, error) {
	if !canonicalUUID(subscription) || !validDevicePreference(p) {
		return DevicePreference{}, store.Invalid{Message: "Ungültige Geräteeinstellung"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.managementBudget(ctx, id, "notification-device-preference-v1", 30); err != nil {
		return DevicePreference{}, err
	}
	tx, err := s.begin(ctx, id)
	if err != nil {
		return DevicePreference{}, err
	}
	defer rollback(tx)
	row, err := loadSubscription(ctx, tx, id.HouseholdID, id.UserID, subscription, true)
	if err != nil {
		return DevicePreference{}, err
	}
	data, err := s.open(row)
	if err != nil {
		return DevicePreference{}, err
	}
	current, err := currentEnvelope(ctx, tx, data)
	if err != nil {
		return DevicePreference{}, err
	}
	if !current || row.disabled != nil || !data.ExpiresAt.After(time.Now()) || data.VAPIDKeyID != s.vapidID {
		return DevicePreference{}, store.ErrConflict
	}
	var version int64
	err = tx.QueryRow(ctx, `SELECT version FROM core.notification_device_preferences WHERE subscription_id=$1 AND category=$2 FOR UPDATE`, subscription, p.Category).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DevicePreference{}, err
	}
	if version != p.Version {
		return DevicePreference{}, store.ErrConflict
	}
	err = tx.QueryRow(ctx, `INSERT INTO core.notification_device_preferences(subscription_id,household_id,user_id,category,enabled,paused,name,enabled_since)
 VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $5 THEN clock_timestamp() END)
 ON CONFLICT(subscription_id,category) DO UPDATE SET enabled=excluded.enabled,paused=excluded.paused,name=excluded.name,
 version=core.notification_device_preferences.version+1,updated_at=clock_timestamp(),
 enabled_since=CASE WHEN NOT excluded.enabled THEN NULL WHEN core.notification_device_preferences.enabled AND NOT core.notification_device_preferences.paused THEN core.notification_device_preferences.enabled_since ELSE clock_timestamp() END
 RETURNING version,enabled_since`, subscription, id.HouseholdID, id.UserID, p.Category, p.Enabled, p.Paused, strings.TrimSpace(p.Name)).Scan(&p.Version, &p.EnabledSince)
	if err != nil {
		return DevicePreference{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE core.notification_device_preferences SET name=$2,updated_at=clock_timestamp() WHERE subscription_id=$1 AND name IS DISTINCT FROM $2`, subscription, strings.TrimSpace(p.Name)); err != nil {
		return DevicePreference{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DevicePreference{}, err
	}
	p.Name = strings.TrimSpace(p.Name)
	return p, nil
}

func (s *Service) DeliveryTarget(ctx context.Context, id auth.Identity, delivery string) (string, error) {
	if !canonicalUUID(delivery) {
		return "", store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.begin(ctx, id)
	if err != nil {
		return "", err
	}
	defer rollback(tx)
	var intent string
	err = tx.QueryRow(ctx, `SELECT intent_id::text FROM core.notification_deliveries WHERE id=$1 AND household_id=$2 AND user_id=$3`, delivery, id.HouseholdID, id.UserID).Scan(&intent)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	i, err := store.ReadNotificationIntent(ctx, tx, intent)
	if err != nil {
		return "", err
	}
	eligible, err := s.store.NotificationAudience(ctx, tx, i)
	if err != nil {
		return "", err
	}
	if i.Category == "expenses" {
		cutoff, e := s.store.FinanceNotificationAuthority(ctx, tx, id, i)
		if e != nil {
			return "", e
		}
		eligible = !cutoff.IsZero()
	}
	target := "/notifications"
	if eligible {
		target = store.NotificationTarget(i)
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return target, nil
}

func (s *Service) Delivery(ctx context.Context, id auth.Identity, delivery string) (DeliveryView, error) {
	if !canonicalUUID(delivery) {
		return DeliveryView{}, store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.begin(ctx, id)
	if err != nil {
		return DeliveryView{}, err
	}
	defer rollback(tx)
	var v DeliveryView
	err = tx.QueryRow(ctx, `SELECT id::text,state,safe_code,accepted_at,updated_at FROM core.notification_deliveries
 WHERE id=$1 AND household_id=$2 AND user_id=$3`, delivery, id.HouseholdID, id.UserID).Scan(&v.ID, &v.State, &v.SafeCode, &v.AcceptedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, store.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err = tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}

// A display acknowledgement is explicitly weaker than a user's seen action.
// Neither changes the underlying task/timer/payment or any module state.
func (s *Service) AcknowledgeDelivery(ctx context.Context, id auth.Identity, delivery string, seen bool) error {
	if !canonicalUUID(delivery) {
		return store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.begin(ctx, id)
	if err != nil {
		return err
	}
	defer rollback(tx)
	code := "display_requested"
	if seen {
		code = "user_confirmed"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.notification_deliveries SET safe_code=CASE WHEN safe_code='user_confirmed' THEN safe_code ELSE $4 END,
 updated_at=CASE WHEN safe_code='user_confirmed' THEN updated_at ELSE clock_timestamp() END
 WHERE id=$1 AND household_id=$2 AND user_id=$3 AND state='accepted'`, delivery, id.HouseholdID, id.UserID, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return store.ErrConflict
	}
	return tx.Commit(ctx)
}
