package web

import (
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/securityevent"
	"household/internal/store"
	"household/internal/value"
)

func (s *Server) securityPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Sicherheit")
	p.View = "security"
	status, err := s.Auth.SecurityStatus(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	keys, err := s.Auth.ListPasskeys(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	keyViews := make([]store.Record, 0, len(keys))
	for _, key := range keys {
		lastUsed := ""
		if key.LastUsedAt != nil {
			lastUsed = securityTime(requestLocale(r), *key.LastUsedAt)
		}
		keyViews = append(keyViews, store.Record{"ID": key.ID, "Name": key.Name,
			"CreatedAt": securityTime(requestLocale(r), key.CreatedAt), "LastUsedAt": lastUsed})
	}
	p.Extra = map[string]any{"totp": status.TOTPEnabled, "recovery_remaining": status.RecoveryRemaining, "passkeys": keyViews, "passkeys_available": s.Auth.PasskeysAvailable(), "recent": identity(r).Recent()}
	rows, err := s.Pool.Query(r.Context(), "SELECT id,user_agent,created_at,last_seen_at,idle_expires_at FROM core.sessions WHERE user_id=$1 AND revoked_at IS NULL AND idle_expires_at>now() AND absolute_expires_at>now() AND auth_version=$2 ORDER BY created_at DESC LIMIT 100", identity(r).UserID, identity(r).AuthVersion)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, ua string
		var created, last, expires time.Time
		if err = rows.Scan(&id, &ua, &created, &last, &expires); err != nil {
			s.fail(w, r, err)
			return
		}
		p.Records = append(p.Records, store.Record{"id": id, "user_agent": ua, "created_at": securityTime(requestLocale(r), created), "last_seen_at": securityTime(requestLocale(r), last), "current": id == identity(r).SessionID})
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, p)
}

func securityTime(locale string, at time.Time) string {
	if locale == "en" {
		return at.UTC().Format("02 Jan 2006 15:04 UTC")
	}
	return at.UTC().Format("02.01.2006 15:04 UTC")
}
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	if !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if err := s.Auth.RevokeSession(r.Context(), identity(r), r.PathValue("id"), "session_revoked"); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}
func (s *Server) setupTOTP(w http.ResponseWriter, r *http.Request) {
	secret, uri, err := s.Auth.SetupTOTP(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Zweiten Faktor einrichten")
	p.View = "totp"
	p.Extra = map[string]any{"secret": secret, "uri": uri}
	s.render(w, r, p)
}
func (s *Server) enableTOTP(w http.ResponseWriter, r *http.Request) {
	codes, err := s.Auth.EnableTOTP(r.Context(), identity(r), r.FormValue("code"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Wiederherstellungscodes")
	p.View = "recovery"
	p.Extra = codes
	s.render(w, r, p)
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	if identity(r).Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	p := s.page(r, "Benutzerverwaltung")
	p.View = "users"
	p.Extra = map[string]any{"recent": identity(r).Recent()}
	rows, err := s.Pool.Query(r.Context(), "SELECT u.id,u.username,u.display_name,m.role,u.disabled_at IS NOT NULL FROM core.users u JOIN core.memberships m ON m.user_id=u.id WHERE m.household_id=$1 ORDER BY u.username LIMIT 100", identity(r).HouseholdID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, user, name, role string
		var disabled bool
		if err = rows.Scan(&id, &user, &name, &role, &disabled); err != nil {
			s.fail(w, r, err)
			return
		}
		p.Records = append(p.Records, store.Record{"id": id, "username": user, "display_name": name, "role": role, "disabled": disabled})
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, p)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	if !id.Recent() {
		s.fail(w, r, auth.ErrRecent)
		return
	}
	username, name := strings.TrimSpace(r.FormValue("username")), strings.TrimSpace(r.FormValue("display_name"))
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`).MatchString(username) || !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		s.fail(w, r, store.Invalid{Message: "Benutzername: 3 bis 64 Buchstaben, Ziffern, Punkt, Bindestrich oder Unterstrich. Anzeigename: 1 bis 100 Zeichen."})
		return
	}
	role := r.FormValue("role")
	if role != "admin" && role != "editor" && role != "reader" || !auth.PasswordValid(r.FormValue("password")) {
		s.fail(w, r, store.Invalid{Message: "Rolle und Startpasswort prüfen (mindestens 14 Zeichen)"})
		return
	}
	hash, err := s.Auth.HashPassword(r.Context(), id.UserID, r.FormValue("password"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err = tx.Exec(r.Context(), "SELECT id FROM core.households WHERE id=$1 FOR UPDATE", id.HouseholdID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = auth.LockRecentSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	var count int
	if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM core.memberships WHERE household_id=$1", id.HouseholdID).Scan(&count); err != nil {
		s.fail(w, r, err)
		return
	}
	if count >= 100 {
		s.fail(w, r, store.Invalid{Message: "Ein Haushalt kann höchstens 100 Benutzer enthalten."})
		return
	}
	var uid string
	err = tx.QueryRow(r.Context(), "INSERT INTO core.users(username,display_name,password_hash) VALUES($1,$2,$3) RETURNING id", username, name, string(hash)).Scan(&uid)
	if err == nil {
		_, err = tx.Exec(r.Context(), "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,$3)", id.HouseholdID, uid, role)
	}
	if err == nil {
		err = store.Audit(r.Context(), tx, id, "core", "users", uid, "user_create", trace(r))
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/users", http.StatusSeeOther)
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	target := r.PathValue("id")
	role := r.FormValue("role")
	disabled := r.FormValue("disabled") == "true"
	if id.Role != "admin" || !value.ValidUUID(target) {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	if !id.Recent() {
		s.fail(w, r, auth.ErrRecent)
		return
	}
	if role != "admin" && role != "editor" && role != "reader" {
		s.fail(w, r, store.Invalid{Message: "ungültige Rolle"})
		return
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err = tx.Exec(r.Context(), "SELECT id FROM core.households WHERE id=$1 FOR UPDATE", id.HouseholdID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = auth.LockRecentSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	var oldRole string
	var oldDisabled bool
	if err = tx.QueryRow(r.Context(), `SELECT m.role,u.disabled_at IS NOT NULL FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.user_id=$2 FOR UPDATE OF m,u`, id.HouseholdID, target).Scan(&oldRole, &oldDisabled); err != nil {
		s.fail(w, r, err)
		return
	}
	if oldRole == role && oldDisabled == disabled {
		if err = tx.Commit(r.Context()); err != nil {
			s.fail(w, r, err)
			return
		}
		http.Redirect(w, r, "/settings/users", http.StatusSeeOther)
		return
	}
	if oldRole == "admin" && (role != "admin" || disabled) {
		var admins int
		err = tx.QueryRow(r.Context(), "SELECT count(*) FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL", id.HouseholdID).Scan(&admins)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if admins <= 1 {
			s.fail(w, r, store.Invalid{Message: "Der letzte aktive Administrator muss erhalten bleiben"})
			return
		}
	}
	if role == "reader" || disabled {
		var responsible bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM core.financial_policies p JOIN core.memberships m
 ON m.household_id=p.household_id AND m.id=p.responsible_membership_id WHERE p.household_id=$1 AND m.user_id=$2 AND p.retired_at IS NULL)`,
			id.HouseholdID, target).Scan(&responsible)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if responsible {
			s.fail(w, r, store.Invalid{Message: "Finanzverantwortung vor Deaktivierung oder Wechsel zur Leserrolle ausdrücklich übertragen; bei fehlendem Inhaltszugriff die administrative Neuzuordnung verwenden"})
			return
		}
	}
	if _, err = tx.Exec(r.Context(), "UPDATE core.memberships SET role=$3,version=version+1,updated_at=now() WHERE household_id=$1 AND user_id=$2", id.HouseholdID, target, role); err == nil {
		_, err = tx.Exec(r.Context(), "UPDATE core.users SET disabled_at=CASE WHEN $2 THEN now() END,auth_version=auth_version+1,updated_at=now() WHERE id=$1", target, disabled)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1", target)
	}
	if err == nil {
		err = store.Audit(r.Context(), tx, id, "core", "users", target, "user_update", trace(r))
	}
	if err == nil {
		err = securityevent.Record(r.Context(), tx, id.HouseholdID, target, "account_changed")
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/users", http.StatusSeeOther)
}
