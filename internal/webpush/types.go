// Package webpush manages explicit personal push connections and bounded delivery.
package webpush

import (
	"errors"
	"time"
)

var ErrLimited = errors.New("push request budget exhausted")
var ErrUnavailable = errors.New("push service is not configured")

const (
	DeviceLimit = 5
	maxCipher   = 16 << 10
	maxHistory  = 128
)

type PersonalContext struct {
	UserID       string `json:"user_id"`
	HouseholdID  string `json:"household_id"`
	SessionScope string `json:"session_scope"`
	AuthScope    string `json:"auth_scope"`
	Role         string `json:"role"`
}

type Context struct {
	Context              PersonalContext `json:"context"`
	State                string          `json:"state"`
	ApplicationServerKey *string         `json:"application_server_key"`
	VAPIDKeyID           *string         `json:"vapid_key_id"`
	DeviceLimit          int             `json:"device_limit"`
}

type Configuration struct {
	State                string  `json:"state"`
	ApplicationServerKey *string `json:"application_server_key"`
	VAPIDKeyID           *string `json:"vapid_key_id"`
	DeviceLimit          int     `json:"device_limit"`
}

type ReceiverKeys struct {
	P256DH string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type Receiver struct {
	Endpoint string       `json:"endpoint"`
	Keys     ReceiverKeys `json:"keys"`
}

type RegisterInput struct {
	DeviceID     string   `json:"device_id"`
	VAPIDKeyID   string   `json:"vapid_key_id"`
	Subscription Receiver `json:"subscription"`
}

type SubscriptionView struct {
	ID           string    `json:"id"`
	DeviceID     string    `json:"device_id"`
	State        string    `json:"state"`
	VAPIDKeyID   string    `json:"vapid_key_id"`
	RegisteredAt time.Time `json:"registered_at"`
}

type Subscriptions struct {
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

type Disabled struct {
	State string `json:"state"`
}

type envelope struct {
	Schema       int       `json:"schema"`
	ID           string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	UserID       string    `json:"user_id"`
	DeviceID     string    `json:"device_id"`
	SessionID    string    `json:"session_id"`
	AuthVersion  int64     `json:"auth_version"`
	Role         string    `json:"role"`
	SessionScope string    `json:"session_scope"`
	AuthScope    string    `json:"auth_scope"`
	RegisteredAt time.Time `json:"registered_at"`
	ConsentAt    time.Time `json:"consent_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	VAPIDKeyID   string    `json:"vapid_key_id"`
	Subscription Receiver  `json:"subscription"`
}

type pushPayload struct {
	Schema        int    `json:"schema"`
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	ExpiresAt     int64  `json:"expires_at"`
	AudienceScope string `json:"audience_scope"`
}
