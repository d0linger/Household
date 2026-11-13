package web

import (
	"encoding/json"
	"net/http"
	"time"

	"household/internal/auth"
	"household/internal/store"
)

func (s *Server) registerSecurityRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/security/reauth", s.require(s.reauthPage))
	mux.Handle("POST /settings/security/reauth", s.require(s.reauthenticate))
	mux.Handle("POST /settings/security/totp/disable", s.require(s.disableTOTP))
	mux.Handle("POST /settings/security/totp/recovery", s.require(s.regenerateRecovery))
	mux.Handle("POST /settings/security/sessions/revoke-others", s.require(s.revokeOtherSessions))
	mux.Handle("POST /settings/security/passkeys/{id}/rename", s.require(s.renamePasskey))
	mux.Handle("POST /settings/security/passkeys/{id}/delete", s.require(s.deletePasskey))
	mux.Handle("GET /api/v1/security/freshness", s.require(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"recent": identity(r).Recent()})
	}))
	mux.HandleFunc("POST /api/v1/auth/passkeys/login/{step}", s.passkeyLogin)
	mux.Handle("POST /api/v1/security/passkeys/{purpose}/{step}", s.require(s.passkeyAuthenticated))
}

func securityReturn(raw string) string {
	if raw == "/settings/users" {
		return raw
	}
	return "/settings/security"
}

func (s *Server) reauthPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Anmeldung bestätigen")
	p.View = "reauth"
	p.Extra = map[string]any{"return_to": securityReturn(r.URL.Query().Get("return_to")), "passkeys_available": s.Auth.PasskeysAvailable()}
	s.render(w, r, p)
}

func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) {
	if len(r.FormValue("password")) > 72 || len(r.FormValue("code")) > 64 {
		s.fail(w, r, auth.ErrCredentials)
		return
	}
	if err := s.Auth.Reauthenticate(r.Context(), identity(r), r.FormValue("password"), r.FormValue("code")); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Header.Get("Accept") == "application/json" {
		writeJSON(w, 200, map[string]bool{"confirmed": true})
		return
	}
	http.Redirect(w, r, securityReturn(r.FormValue("return_to")), http.StatusSeeOther)
}

func (s *Server) disableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm") != "disable" {
		s.fail(w, r, store.Invalid{Message: "Bestätige das Deaktivieren der Zwei-Faktor-Anmeldung."})
		return
	}
	if err := s.Auth.DisableTOTP(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}
func (s *Server) regenerateRecovery(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm") != "renew" {
		s.fail(w, r, store.Invalid{Message: "Bestätige, dass die bisherigen Wiederherstellungscodes ungültig werden."})
		return
	}
	codes, err := s.Auth.RegenerateRecoveryCodes(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Wiederherstellungscodes")
	p.View = "recovery"
	p.Extra = codes
	s.render(w, r, p)
}
func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.RevokeOtherSessions(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}
func (s *Server) renamePasskey(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.ChangePasskey(r.Context(), identity(r), r.PathValue("id"), r.FormValue("name"), false); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}
func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm") != "delete" {
		s.fail(w, r, store.Invalid{Message: "Bestätige das Entfernen dieses Passkeys."})
		return
	}
	if err := s.Auth.ChangePasskey(r.Context(), identity(r), r.PathValue("id"), "", true); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}

func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("household_login")
	if err != nil || !s.Auth.ValidCSRF(cookie.Value, "login", r.Header.Get("X-CSRF-Token")) {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	s.passkeyCeremony(w, r, auth.Identity{}, "login", cookie.Value)
}
func (s *Server) passkeyAuthenticated(w http.ResponseWriter, r *http.Request) {
	purpose := r.PathValue("purpose")
	if purpose != "register" && purpose != "reauth" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	cookie, err := r.Cookie(s.cookieName())
	if err != nil {
		s.fail(w, r, auth.ErrSession)
		return
	}
	s.passkeyCeremony(w, r, identity(r), purpose, cookie.Value)
}
func (s *Server) passkeyCeremony(w http.ResponseWriter, r *http.Request, id auth.Identity, purpose, binding string) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	if r.PathValue("step") == "begin" {
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(w, r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		out, err := s.Auth.BeginPasskey(r.Context(), id, purpose, binding, s.clientIP(r), in.Name)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	if r.PathValue("step") != "finish" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	var in struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
	}
	if err := readJSON(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	raw, resolved, err := s.Auth.FinishPasskey(r.Context(), id, purpose, binding, s.clientIP(r), r.UserAgent(), in.ChallengeID, in.Credential)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if purpose == "login" {
		s.setCookie(w, raw, resolved)
		http.SetCookie(w, s.cookie("household_login", "", -1, time.Time{}))
	}
	writeJSON(w, 200, map[string]bool{"confirmed": true})
}
