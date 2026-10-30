package store

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func shoppingPreferenceView(scope ShoppingExtensionContext, list ShoppingPreferenceList, settings shoppingNotifySettings) ShoppingPreferenceView {
	out := ShoppingPreferenceView{Context: scope, List: list, Revision: "0"}
	if user, ok := settings.Users[scope.UserID]; ok {
		out.Revision = user.Revision
		if watch, exists := user.Watches[list.ID]; exists {
			out.Preference = &watch
		}
	}
	return out
}

func (s *Store) ShoppingNotificationPreferences(ctx context.Context, id auth.Identity, listID string, generation int64) (ShoppingPreferenceView, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	defer shoppingRollback(tx)
	scope, err := shoppingScope(ctx, tx, id)
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	if generation != scope.Generation {
		return ShoppingPreferenceView{}, ErrConflict
	}
	list, err := shoppingReadList(ctx, tx, id.HouseholdID, listID)
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	settings, err := shoppingReadSettings(ctx, tx, id.HouseholdID, generation)
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	out := shoppingPreferenceView(scope, list, settings)
	return out, tx.Commit(ctx)
}

func (s *Store) ShoppingNotificationOverview(ctx context.Context, id auth.Identity, generation int64) (ShoppingPreferenceOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := ShoppingPreferenceOverview{Revision: "0", Lists: []ShoppingPreferenceEntry{}}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer shoppingRollback(tx)
	out.Context, err = shoppingScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if generation != out.Context.Generation {
		return out, ErrConflict
	}
	settings, err := shoppingReadSettings(ctx, tx, id.HouseholdID, generation)
	if err != nil {
		return out, err
	}
	if user, ok := settings.Users[id.UserID]; ok {
		out.Revision = user.Revision
		ids := make([]string, 0, len(user.Watches))
		for lid := range user.Watches {
			ids = append(ids, lid)
		}
		sort.Strings(ids)
		for _, lid := range ids {
			list, e := shoppingReadList(ctx, tx, id.HouseholdID, lid)
			if e == ErrNotFound {
				continue
			}
			if e != nil {
				return out, e
			}
			watch := user.Watches[lid]
			out.Lists = append(out.Lists, ShoppingPreferenceEntry{List: list, Preference: &watch})
		}
	}
	return out, tx.Commit(ctx)
}

func (s *Store) SetShoppingNotificationPreferences(ctx context.Context, id auth.Identity, listID, requestID string, in ShoppingPreferenceInput) (ShoppingPreferenceView, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx = audit.WithRequest(ctx, requestID)
	if _, err := shoppingRevision(in.Revision); err != nil {
		return ShoppingPreferenceView{}, err
	}
	if in.MuteForSeconds != 0 && in.MuteForSeconds != 3600 && in.MuteForSeconds != 86400 && in.MuteForSeconds != 604800 ||
		!in.Enabled && (in.PushEnabled || in.MuteForSeconds != 0) {
		return ShoppingPreferenceView{}, Invalid{"Benachrichtigungswahl ausdrücklich prüfen"}
	}
	tx, scope, err := s.shoppingPreferenceTransaction(ctx, id)
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	defer shoppingRollback(tx)
	if scope.Generation != in.Generation {
		return ShoppingPreferenceView{}, ErrConflict
	}
	list, err := shoppingReadList(ctx, tx, id.HouseholdID, strings.ToLower(listID))
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	settings, err := shoppingReadSettings(ctx, tx, id.HouseholdID, in.Generation)
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	view := shoppingPreferenceView(scope, list, settings)
	if in.Revision != view.Revision {
		return ShoppingPreferenceView{}, ErrConflict
	}
	user, exists := settings.Users[id.UserID]
	if !exists {
		user = shoppingNotifyUser{Revision: "0", Watches: map[string]ShoppingPreference{}}
	}
	old, present := user.Watches[list.ID]
	if !in.Enabled && !present {
		return view, tx.Commit(ctx)
	}
	if in.Enabled && (list.Archived || list.IsTemplate) {
		return ShoppingPreferenceView{}, Invalid{"Nur aktive Einkaufslisten abonnieren"}
	}
	revision, _ := shoppingRevision(user.Revision)
	if revision == math.MaxUint64 {
		return ShoppingPreferenceView{}, Invalid{"Revisionsgrenze erreicht"}
	}
	// Consent begins after the module UPDATE lock, using database wall time.
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return ShoppingPreferenceView{}, err
	}
	now = now.UTC()
	if !in.Enabled {
		delete(user.Watches, list.ID)
	} else {
		watch := ShoppingPreference{Enabled: true, PushEnabled: in.PushEnabled, EnabledSince: now}
		if present {
			watch.EnabledSince = old.EnabledSince
		}
		// Explicit resume starts a fresh consent boundary; work from the muted
		// interval must not reappear after clearing its deadline.
		resumed := present && old.MutedUntil != nil && in.MuteForSeconds == 0
		if resumed {
			watch.EnabledSince = now
		}
		if in.PushEnabled {
			since := now
			if present && !resumed && old.PushEnabled && old.PushEnabledSince != nil {
				since = *old.PushEnabledSince
			}
			watch.PushEnabledSince = &since
		}
		if in.MuteForSeconds > 0 {
			until := now.Add(time.Duration(in.MuteForSeconds) * time.Second)
			watch.MutedUntil = &until
		}
		user.Watches[list.ID] = watch
	}
	user.Revision = strconv.FormatUint(revision+1, 10)
	settings.Users[id.UserID] = user
	if err = shoppingValidateSettings(settings, in.Generation); err != nil {
		return ShoppingPreferenceView{}, err
	}
	err = shoppingWriteSettings(ctx, tx, id.HouseholdID, settings)
	if err != nil {
		return ShoppingPreferenceView{}, err
	}
	scope.ServerTime = now
	view = shoppingPreferenceView(scope, list, settings)
	return view, tx.Commit(ctx)
}
