package store

import "time"

const (
	ShoppingDigestJobKind        = "shopping_digest_v1"
	ShoppingPushJobKind          = "shopping_push_v1"
	shoppingNotificationKind     = "shopping_list_changed_v1"
	shoppingNotificationConsumer = "shopping-list-notifications-v1"
	shoppingNotifyNamespace      = "shopping_notify_v1"
	shoppingNotifyBytes          = 128 << 10
	shoppingSnapshotBytes        = 2 << 20
	shoppingSnapshotItems        = 1000
	shoppingSafeInteger          = int64(9007199254740991)
)

type ShoppingExtensionContext struct {
	UserID         string    `json:"user_id"`
	HouseholdID    string    `json:"household_id"`
	Generation     int64     `json:"generation"`
	SessionScope   string    `json:"session_scope"`
	AuthScope      string    `json:"auth_scope"`
	Role           string    `json:"role"`
	CanWrite       bool      `json:"can_write"`
	OfflineAllowed bool      `json:"offline_allowed"`
	ServerTime     time.Time `json:"server_time"`
}

type ShoppingPreferenceInput struct {
	Generation     int64  `json:"generation"`
	Revision       string `json:"revision"`
	Enabled        bool   `json:"enabled"`
	PushEnabled    bool   `json:"push_enabled"`
	MuteForSeconds int64  `json:"mute_for_seconds"`
}

type ShoppingPreference struct {
	Enabled          bool       `json:"enabled"`
	PushEnabled      bool       `json:"push_enabled"`
	MutedUntil       *time.Time `json:"muted_until"`
	EnabledSince     time.Time  `json:"enabled_since"`
	PushEnabledSince *time.Time `json:"push_enabled_since"`
}

type ShoppingPreferenceList struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Archived   bool   `json:"archived"`
	IsTemplate bool   `json:"is_template"`
}

type ShoppingPreferenceEntry struct {
	List       ShoppingPreferenceList `json:"list"`
	Preference *ShoppingPreference    `json:"preference"`
}

type ShoppingPreferenceView struct {
	Context    ShoppingExtensionContext `json:"context"`
	List       ShoppingPreferenceList   `json:"list"`
	Revision   string                   `json:"revision"`
	Preference *ShoppingPreference      `json:"preference"`
}

type ShoppingPreferenceOverview struct {
	Context  ShoppingExtensionContext  `json:"context"`
	Revision string                    `json:"revision"`
	Lists    []ShoppingPreferenceEntry `json:"lists"`
}

type shoppingNotifySettings struct {
	Schema     int                           `json:"schema"`
	Generation int64                         `json:"generation"`
	Users      map[string]shoppingNotifyUser `json:"users"`
}

type shoppingNotifyUser struct {
	Revision string                        `json:"revision"`
	Watches  map[string]ShoppingPreference `json:"watches"`
}

type ShoppingSnapshotInput struct {
	Generation int64    `json:"generation"`
	ListIDs    []string `json:"list_ids"`
}

type ShoppingSnapshotItem struct {
	ID               string  `json:"id"`
	Version          string  `json:"version"`
	Name             string  `json:"name"`
	Quantity         *string `json:"quantity"`
	Unit             *string `json:"unit"`
	Group            string  `json:"group"`
	Note             string  `json:"note"`
	Checked          bool    `json:"checked"`
	Availability     string  `json:"availability"`
	AssigneeLabel    *string `json:"assignee_label"`
	ProductLabel     *string `json:"product_label"`
	PackLabel        *string `json:"pack_label"`
	ReplacementLabel *string `json:"replacement_label"`
}

type ShoppingSnapshotList struct {
	ID         string                 `json:"id"`
	Version    string                 `json:"version"`
	Name       string                 `json:"name"`
	Archived   bool                   `json:"archived"`
	IsTemplate bool                   `json:"is_template"`
	Items      []ShoppingSnapshotItem `json:"items"`
}

type ShoppingSnapshotTotals struct {
	Lists     int `json:"lists"`
	Items     int `json:"items"`
	UTF8Bytes int `json:"utf8_bytes,omitempty"`
}

type ShoppingSnapshot struct {
	Schema      int                      `json:"schema"`
	Context     ShoppingExtensionContext `json:"context"`
	CapturedAt  time.Time                `json:"captured_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
	Complete    bool                     `json:"complete"`
	Lists       []ShoppingSnapshotList   `json:"lists"`
	Totals      ShoppingSnapshotTotals   `json:"totals"`
	Fingerprint string                   `json:"fingerprint,omitempty"`
}

// ShoppingNotificationReference is a closed authority reference, never a push endpoint.
type ShoppingNotificationReference struct {
	Schema           int       `json:"schema"`
	HouseholdID      string    `json:"household_id"`
	RecipientID      string    `json:"recipient_id"`
	ListID           string    `json:"list_id"`
	Generation       int64     `json:"generation"`
	WatchRevision    string    `json:"watch_revision"`
	EventAt          time.Time `json:"event_at"`
	PushEnabledSince time.Time `json:"push_enabled_since"`
	Bucket           int64     `json:"bucket"`
	NotificationID   string    `json:"notification_id,omitempty"`
	SubscriptionID   string    `json:"subscription_id,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	Deleted          bool      `json:"deleted"`
}

type ShoppingAudience struct {
	Eligible     bool
	PushEligible bool
}

type ShoppingInboxNotice struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	TargetPath string `json:"target_path"`
}
