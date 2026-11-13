// Package notification defines the closed, content-free contract shared by
// module producers and device delivery. Modules retain ownership of eligibility.
package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/value"
)

const Schema = 1
const JobKind = "notification_push_v1"
const MaxRevision int64 = 9007199254740991

// Policy is compiled, never supplied by a job or by a browser.
type Policy struct {
	Category  string
	Module    string
	Freshness time.Duration
	Attempts  int
}

var policies = [...]Policy{
	{"chores_due", "chores", time.Hour, 5},
	{"chores_digest", "chores", 4 * time.Hour, 5},
	{"chores_escalation", "chores", time.Hour, 5},
	{"cooking_timer", "recipes", time.Minute, 2},
	{"shopping", "shopping", 24 * time.Hour, 5},
	{"pantry", "pantry", 24 * time.Hour, 5},
	{"fuel", "fuel", 24 * time.Hour, 5},
	{"expenses", "expenses", 24 * time.Hour, 5},
	{"meters", "meters", 24 * time.Hour, 5},
	{"documents", "documents", 24 * time.Hour, 5},
	{"mealplan", "mealplan", 4 * time.Hour, 5},
	{"security", "", time.Hour, 5},
	{"test", "", time.Minute, 2},
}

func Categories() []Policy { return append([]Policy(nil), policies[:]...) }

func Lookup(category string) (Policy, bool) {
	for _, p := range policies {
		if p.Category == category {
			return p, true
		}
	}
	return Policy{}, false
}

// Intent carries references only. Titles, amounts, secrets, paths and arbitrary
// payloads cannot cross this contract. Occurrence identifies a business episode.
type Intent struct {
	Schema       int       `json:"schema"`
	HouseholdID  string    `json:"household_id"`
	UserID       string    `json:"user_id"`
	Module       string    `json:"module"`
	Generation   int64     `json:"generation"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Revision     int64     `json:"revision"`
	Occurrence   string    `json:"occurrence"`
	Category     string    `json:"category"`
	EventAt      time.Time `json:"event_at"`
	NotBefore    time.Time `json:"not_before"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func canonicalID(id string) bool { return value.ValidUUID(id) && strings.ToLower(id) == id }

func identifier(s string, max int) bool {
	if len(s) < 1 || len(s) > max {
		return false
	}
	for _, c := range s {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == ':' || c == '.' || c == '/'
		if !allowed {
			return false
		}
	}
	return true
}

func (i Intent) Validate() error {
	p, ok := Lookup(i.Category)
	validTime := !i.EventAt.IsZero() && i.EventAt.Year() >= 2000 && i.ExpiresAt.Year() <= 9999 &&
		!i.NotBefore.Before(i.EventAt) && i.ExpiresAt.After(i.NotBefore) && i.ExpiresAt.Sub(i.NotBefore) <= p.Freshness
	if !ok || i.Schema != Schema || i.Module != p.Module || !canonicalID(i.HouseholdID) || !canonicalID(i.UserID) ||
		!canonicalID(i.ResourceID) || i.Revision < 1 || i.Revision > MaxRevision || i.Generation < 0 || i.Generation > MaxRevision || (i.Module != "" && i.Generation < 1) ||
		(i.Module == "" && i.Generation != 0) || !identifier(i.ResourceType, 64) || !identifier(i.Occurrence, 160) || !validTime {
		return errors.New("invalid notification intent")
	}
	return nil
}

// DedupeKey excludes scheduling timestamps: changing a deadline must advance the
// source revision; a concurrent repeat of one business event stays one intent.
func (i Intent) DedupeKey() string {
	raw, _ := json.Marshal([]any{Schema, i.HouseholdID, i.UserID, i.Module, i.Generation,
		i.ResourceType, i.ResourceID, i.Revision, i.Occurrence, i.Category})
	hash := sha256.Sum256(raw)
	return "notification-v1/" + hex.EncodeToString(hash[:])
}

type QuietHours struct {
	Timezone string `json:"timezone"`
	Start    *int   `json:"quiet_start"`
	End      *int   `json:"quiet_end"`
}

func (q QuietHours) Validate() error {
	if len(q.Timezone) > 100 || q.Timezone == "" || q.Timezone == "Local" {
		return errors.New("invalid notification timezone")
	}
	if _, err := time.LoadLocation(q.Timezone); err != nil {
		return errors.New("invalid notification timezone")
	}
	if (q.Start == nil) != (q.End == nil) {
		return errors.New("incomplete quiet hours")
	}
	if q.Start != nil && (*q.Start < 0 || *q.Start > 1439 || *q.End < 0 || *q.End > 1439 || *q.Start == *q.End) {
		return errors.New("invalid quiet hours")
	}
	return nil
}

// NextAllowed walks actual UTC minutes across DST transitions instead of using
// time.Date's ambiguous/nonexistent local-time resolution. The scan is bounded.
func (q QuietHours) NextAllowed(at time.Time) (time.Time, error) {
	if err := q.Validate(); err != nil {
		return time.Time{}, err
	}
	if q.Start == nil {
		return at, nil
	}
	loc, _ := time.LoadLocation(q.Timezone)
	quiet := func(t time.Time) bool {
		local := t.In(loc)
		minute := local.Hour()*60 + local.Minute()
		if *q.Start < *q.End {
			return minute >= *q.Start && minute < *q.End
		}
		return minute >= *q.Start || minute < *q.End
	}
	if !quiet(at) {
		return at, nil
	}
	next := at.Truncate(time.Minute).Add(time.Minute)
	for range 48 * 60 {
		if !quiet(next) {
			return next, nil
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}, errors.New("quiet hours have no bounded delivery window")
}
