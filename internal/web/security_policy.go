package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/i18n"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

// securityPolicy is the household's second-factor requirement.
type securityPolicy struct {
	RequireAdmin  bool
	RequireEditor bool
	GraceDays     int
	EnforcedFrom  time.Time
	Version       int64
}

// Deadline is the end of the grace period.
func (p securityPolicy) Deadline() time.Time {
	return p.EnforcedFrom.Add(time.Duration(p.GraceDays) * 24 * time.Hour)
}

// Applies reports whether the policy requires a factor for role.
func (p securityPolicy) Applies(role string) bool {
	return role == "admin" && p.RequireAdmin || role == "editor" && p.RequireEditor
}

// policyCacheTTL bounds how stale the per-request policy may be; saving the
// policy invalidates the cache on this instance immediately.
const policyCacheTTL = 30 * time.Second

type policyCache struct {
	mu      sync.Mutex
	loaded  time.Time
	policy  securityPolicy
	present bool
}

func (s *Server) securityPolicy(ctx context.Context, household string) (securityPolicy, error) {
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	if s.policy.present && time.Since(s.policy.loaded) < policyCacheTTL {
		return s.policy.policy, nil
	}
	var p securityPolicy
	var grace int16
	err := s.Pool.QueryRow(ctx, `SELECT require_admin_mfa,require_editor_mfa,grace_days,enforced_from,version FROM core.security_policies WHERE household_id=$1`, household).
		Scan(&p.RequireAdmin, &p.RequireEditor, &grace, &p.EnforcedFrom, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		// A failed refresh must never turn a required factor into an optional one.
		return securityPolicy{}, err
	}
	p.GraceDays = int(grace)
	s.policy.policy, s.policy.loaded, s.policy.present = p, time.Now(), true
	return p, nil
}

func (s *Server) invalidatePolicy() {
	s.policy.mu.Lock()
	s.policy.present = false
	s.policy.mu.Unlock()
}

func (s *Server) hasSecondFactor(ctx context.Context, user string) (bool, error) {
	var has bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.totp_credentials WHERE user_id=$1 AND enabled_at IS NOT NULL)
 OR EXISTS(SELECT 1 FROM core.passkey_credentials WHERE user_id=$1)`, user).Scan(&has)
	return has, err
}

// mfaState reports whether the identity must add a factor, and whether its
// grace period is over so only the security settings remain reachable.
func (s *Server) mfaState(ctx context.Context, id auth.Identity) (required, blocked bool, deadline time.Time, err error) {
	policy, err := s.securityPolicy(ctx, id.HouseholdID)
	if err != nil || !policy.Applies(id.Role) {
		return false, false, time.Time{}, err
	}
	has, err := s.hasSecondFactor(ctx, id.UserID)
	if err != nil || has {
		return false, false, time.Time{}, err
	}
	deadline = policy.Deadline()
	return true, time.Now().After(deadline), deadline, nil
}

// mfaAllowedPath lists what an account blocked by the policy can still reach:
// its security settings (to add a factor), sign-out, password and language.
func mfaAllowedPath(path string) bool {
	return strings.HasPrefix(path, "/settings/security") || strings.HasPrefix(path, "/api/v1/security/") ||
		path == "/logout" || path == "/change-password" || path == "/settings/language" || path == "/api/v1/core/preferences"
}

// enforceSecurityPolicy answers a blocked request; it reports whether it did.
func (s *Server) enforceSecurityPolicy(w http.ResponseWriter, r *http.Request, id auth.Identity) bool {
	if mfaAllowedPath(r.URL.Path) {
		return false
	}
	_, blocked, _, err := s.mfaState(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return true
	}
	if !blocked {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": i18n.Text(requestLocale(r), "Richte zuerst einen zweiten Faktor ein."), "code": "mfa_required"})
	} else {
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
	}
	return true
}

// mfaNotice is the banner shown while a required factor is still missing.
func (s *Server) mfaNotice(r *http.Request, id auth.Identity) string {
	required, blocked, deadline, err := s.mfaState(r.Context(), id)
	if err != nil || !required {
		return ""
	}
	if blocked {
		return i18n.Text(requestLocale(r), "Dein Haushalt verlangt einen zweiten Faktor. Richte ihn jetzt ein, um Household weiter zu verwenden.")
	}
	layout := "02.01.2006"
	if requestLocale(r) == "en" {
		layout = "2006-01-02"
	}
	return i18n.Text(requestLocale(r), "Dein Haushalt verlangt einen zweiten Faktor. Richte ihn bis %s ein.", deadline.Format(layout))
}

// saveSecurityPolicy lets an administrator with a recent sign-in and an own
// second factor change the requirement.
func (s *Server) saveSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	requireAdmin := r.FormValue("require_admin_mfa") == "on"
	requireEditor := r.FormValue("require_editor_mfa") == "on"
	grace, err := strconv.Atoi(r.FormValue("grace_days"))
	if err != nil || grace != 0 && grace != 7 && grace != 14 && grace != 30 || requireEditor && !requireAdmin {
		s.fail(w, r, store.Invalid{Message: "Bitte die Angaben zur Anmelderichtlinie prüfen"})
		return
	}
	version, err := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err != nil || version < 0 {
		s.fail(w, r, store.Invalid{Message: "Bitte die Seite neu laden"})
		return
	}
	if requireAdmin {
		has, err := s.hasSecondFactor(r.Context(), id.UserID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !has {
			s.fail(w, r, store.Invalid{Message: "Richte zuerst selbst einen zweiten Faktor ein"})
			return
		}
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()
	if err = auth.LockRecentSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	// The grace period starts when a requirement is switched on or widened.
	tag, err := tx.Exec(r.Context(), `INSERT INTO core.security_policies(household_id,require_admin_mfa,require_editor_mfa,grace_days,enforced_from,updated_by)
 VALUES($1,$2,$3,$4,now(),$5)
 ON CONFLICT(household_id) DO UPDATE SET require_admin_mfa=excluded.require_admin_mfa,require_editor_mfa=excluded.require_editor_mfa,grace_days=excluded.grace_days,
  enforced_from=CASE WHEN excluded.require_admin_mfa AND NOT core.security_policies.require_admin_mfa OR excluded.require_editor_mfa AND NOT core.security_policies.require_editor_mfa
   THEN now() ELSE core.security_policies.enforced_from END,
  version=core.security_policies.version+1,updated_by=excluded.updated_by,updated_at=now()
 WHERE core.security_policies.version=$6`, id.HouseholdID, requireAdmin, requireEditor, grace, id.UserID, version)
	if err == nil && tag.RowsAffected() == 0 {
		err = store.ErrConflict
	}
	if err == nil {
		err = audit.Event(r.Context(), tx, "core", "security_policies", "", "security_policy_changed", nil)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.invalidatePolicy()
	s.backupDone(w, r, map[string]string{"status": "saved"}, "Anmelderichtlinie gespeichert.")
}
