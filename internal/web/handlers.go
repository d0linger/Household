package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/module"
	"household/internal/store"
	"household/internal/value"
)

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	raw := auth.RandomToken()
	http.SetCookie(w, s.cookie("household_login", raw, 600, time.Time{}))
	p := s.page(r, "Anmelden")
	p.View = "login"
	p.CSRF = s.Auth.CSRF(raw, "login")
	s.render(w, r, p)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, store.Invalid{Message: "ungültiges Formular"})
		return
	}
	challenge, err := r.Cookie("household_login")
	if err != nil || !s.Auth.ValidCSRF(challenge.Value, "login", r.FormValue("csrf")) {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	if len(r.FormValue("username")) > 64 || len(r.FormValue("password")) > 72 || len(r.FormValue("code")) > 64 {
		s.fail(w, r, auth.ErrCredentials)
		return
	}
	raw, id, err := s.Auth.Login(r.Context(), r.FormValue("username"), r.FormValue("password"), r.FormValue("code"), s.clientIP(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setCookie(w, raw, id)
	http.SetCookie(w, s.cookie("household_login", "", -1, time.Time{}))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.Logout(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, s.cookie(s.cookieName(), "", -1, time.Time{}))
	w.Header().Set("Clear-Site-Data", "\"cache\", \"storage\"")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (s *Server) passwordPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Passwort ändern")
	p.View = "password"
	s.render(w, r, p)
}
func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.ChangePassword(r.Context(), identity(r), r.FormValue("current"), r.FormValue("next")); err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, s.cookie(s.cookieName(), "", -1, time.Time{}))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	for _, m := range s.Store.Modules {
		if _, ok := s.Store.State(m.ID); ok {
			http.Redirect(w, r, "/m/"+m.ID+"/"+m.Entities[0].ID, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings/modules", http.StatusSeeOther)
}

func (s *Server) resolve(r *http.Request) (module.Descriptor, module.Entity, error) {
	m, ok := s.Store.Module(r.PathValue("module"))
	if !ok {
		return module.Descriptor{}, module.Entity{}, store.ErrNotFound
	}
	if _, ok = s.Store.State(m.ID); !ok {
		return m, module.Entity{}, store.ErrNotFound
	}
	e, ok := m.Entity(r.PathValue("entity"))
	if !ok {
		return m, e, store.ErrNotFound
	}
	return m, e, nil
}

func (s *Server) modulePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 || page > 2000 {
		page = 0
	}
	query := r.URL.Query().Get("q")
	if len(query) > 100 {
		query = query[:100]
	}
	rows, err := s.Store.List(r.Context(), identity(r), m, e, query, 50, page*50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, m.Title)
	p.Module = m
	p.Entity = e
	p.Records = rows
	p.View = "module"
	p.Page = page
	p.Query = query
	p.Record = prefill(r, e)
	if identity(r).CanWrite() {
		p.References, err = s.references(r, e, p.Record)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.render(w, r, p)
}
func (s *Server) detailPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	record, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, m.Title)
	p.Module = m
	p.Entity = e
	p.Record = record
	p.View = "detail"
	p.Related, err = s.related(r, m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.References, err = s.references(r, e, p.Record)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, p)
}

func (s *Server) mutation(w http.ResponseWriter, r *http.Request) (store.Mutation, string, error) {
	var in store.Mutation
	key := r.Header.Get("Idempotency-Key")
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := readJSON(w, r, &in); err != nil {
			return in, key, err
		}
	} else {
		in.Data = map[string]json.RawMessage{}
		in.TargetGenerations = map[string]int64{}
		in.Version, _ = strconv.ParseInt(r.FormValue("version"), 10, 64)
		in.Generation, _ = strconv.ParseInt(r.FormValue("generation"), 10, 64)
		key = r.FormValue("idempotency_key")
		for name, values := range r.Form {
			if name == "csrf" || name == "version" || name == "generation" || name == "idempotency_key" {
				continue
			}
			if len(values) != 1 {
				return in, key, store.Invalid{Message: "mehrfaches Formularfeld"}
			}
			if target, ok := strings.CutPrefix(name, "target_generation_"); ok {
				generation, err := strconv.ParseInt(values[0], 10, 64)
				if err != nil {
					return in, key, store.Invalid{Message: "ungültiger Modulstand"}
				}
				for _, needed := range store.ActionTargets(r.PathValue("module"), r.PathValue("entity"), r.PathValue("action")) {
					if needed == target {
						in.TargetGenerations[target] = generation
					}
				}
				continue
			}
			raw, err := json.Marshal(values[0])
			if err != nil {
				return in, key, err
			}
			in.Data[name] = raw
		}
	}
	return in, key, nil
}
func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, err := s.Store.List(r.Context(), identity(r), m, e, r.URL.Query().Get("q"), 100, offset)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"records": rows, "offset": offset, "has_more": len(rows) == 100})
}
func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	record, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, record)
}
func (s *Server) createRecord(w http.ResponseWriter, r *http.Request) { s.writeRecord(w, r, "") }
func (s *Server) updateRecord(w http.ResponseWriter, r *http.Request) {
	s.writeRecord(w, r, r.PathValue("id"))
}
func (s *Server) writeRecord(w http.ResponseWriter, r *http.Request, rid string) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in, key, err := s.mutation(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	record, err := s.Store.Write(r.Context(), identity(r), m, e, rid, key, trace(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		http.Redirect(w, r, "/m/"+m.ID+"/"+e.ID, http.StatusSeeOther)
		return
	}
	writeJSON(w, 200, record)
}
func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in, _, err := s.mutation(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.Store.Delete(r.Context(), identity(r), m, e, r.PathValue("id"), in.Version, in.Generation, trace(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) actionRecord(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in, key, err := s.mutation(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	record, err := s.Store.Action(r.Context(), identity(r), m, e, r.PathValue("id"), r.PathValue("action"), key, trace(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, record)
}

func (s *Server) references(r *http.Request, e module.Entity, selected store.Record) (map[string][]store.Record, error) {
	out := map[string][]store.Record{}
	for _, f := range e.Fields {
		if f.Reference == "" {
			continue
		}
		parts := strings.Split(f.Reference, "/")
		if len(parts) != 2 {
			continue
		}
		if parts[0] == "core" {
			if parts[1] == "memberships" {
				rows, err := s.Pool.Query(r.Context(), `SELECT m.id,u.display_name||CASE WHEN m.inactive_at IS NOT NULL OR u.disabled_at IS NOT NULL THEN ' (nicht mehr aktiv)' ELSE '' END
				 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1
				 AND ((m.inactive_at IS NULL AND u.disabled_at IS NULL) OR m.id=nullif($2,'')::uuid)
				 ORDER BY (m.id=nullif($2,'')::uuid) DESC NULLS LAST,u.display_name,m.id LIMIT 100`, identity(r).HouseholdID, store.String(selected[f.Name]))
				if err != nil {
					return nil, err
				}
				list := []store.Record{}
				for rows.Next() {
					var id, name string
					if err = rows.Scan(&id, &name); err != nil {
						rows.Close()
						return nil, err
					}
					list = append(list, store.Record{"id": id, "label": name})
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return nil, err
				}
				out[f.Name] = list
			}
			continue
		}
		m, ok := s.Store.Module(parts[0])
		if !ok {
			continue
		}
		if _, enabled := s.Store.State(m.ID); !enabled {
			continue
		}
		target, ok := m.Entity(parts[1])
		if !ok {
			continue
		}
		records, err := s.Store.List(r.Context(), identity(r), m, target, "", 100, 0)
		if err != nil {
			return nil, err
		}
		// Editing a record must retain its parent even beyond the first page.
		selectedID := store.String(selected[f.Name])
		found := false
		for _, record := range records {
			found = found || store.String(record["id"]) == selectedID
		}
		if selectedID != "" && !found && value.ValidUUID(selectedID) {
			record, getErr := s.Store.Get(r.Context(), identity(r), m, target, selectedID)
			if getErr == nil {
				records = append(records, record)
			} else if !errors.Is(getErr, store.ErrNotFound) {
				return nil, getErr
			}
		}
		for _, record := range records {
			label := store.String(record[target.Primary])
			if m.ID == "shopping" && target.ID == "products" {
				label = shoppingProductLabel(record)
			}
			if label == "" {
				label = target.Title + " · " + store.String(record["id"])[:8]
			}
			record["label"] = label
		}
		out[f.Name] = records
	}
	return out, nil
}
func (s *Server) referencesAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	records, err := s.Store.List(r.Context(), identity(r), m, e, r.URL.Query().Get("q"), 100, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, record := range records {
		record["label"] = record[e.Primary]
		if m.ID == "shopping" && e.ID == "products" {
			record["label"] = shoppingProductLabel(record)
		}
	}
	writeJSON(w, 200, records)
}
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"user": identity(r), "modules": s.Store.Modules, "states": s.Store.States(), "csrf": r.Context().Value(csrfKey), "version": "0.1.0"})
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Einstellungen")
	p.View = "settings"
	s.render(w, r, p)
}
func (s *Server) setModule(w http.ResponseWriter, r *http.Request) {
	version, _ := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err := s.Store.SetModule(r.Context(), identity(r), r.PathValue("module"), r.FormValue("enabled") == "true", version, trace(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/modules", http.StatusSeeOther)
}
func (s *Server) securityPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Sicherheit")
	p.View = "security"
	rows, err := s.Pool.Query(r.Context(), "SELECT id,user_agent,created_at,last_seen_at,idle_expires_at FROM core.sessions WHERE user_id=$1 AND revoked_at IS NULL AND idle_expires_at>now() ORDER BY created_at DESC", identity(r).UserID)
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
		p.Records = append(p.Records, store.Record{"id": id, "user_agent": ua, "created_at": created.Format("02.01.2006 15:04"), "last_seen_at": last.Format("02.01.2006 15:04"), "current": id == identity(r).SessionID})
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, p)
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
	rows, err := s.Pool.Query(r.Context(), "SELECT u.id,u.username,u.display_name,m.role,u.disabled_at IS NOT NULL FROM core.users u JOIN core.memberships m ON m.user_id=u.id WHERE m.household_id=$1 ORDER BY u.username", identity(r).HouseholdID)
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
	if id.Role != "admin" || !id.Recent() {
		s.fail(w, r, store.ErrForbidden)
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
	if err = auth.LockSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	var uid string
	err = tx.QueryRow(r.Context(), "INSERT INTO core.users(username,display_name,password_hash) VALUES($1,$2,$3) RETURNING id", r.FormValue("username"), r.FormValue("display_name"), string(hash)).Scan(&uid)
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
	if id.Role != "admin" || !id.Recent() || !value.ValidUUID(target) {
		s.fail(w, r, store.ErrForbidden)
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
	if err = auth.LockSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	var oldRole string
	if err = tx.QueryRow(r.Context(), "SELECT role FROM core.memberships WHERE household_id=$1 AND user_id=$2", id.HouseholdID, target).Scan(&oldRole); err != nil {
		s.fail(w, r, err)
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
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/users", http.StatusSeeOther)
}

func (s *Server) preferences(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Theme   string `json:"theme"`
		Offline bool   `json:"offline"`
	}
	if err := readJSON(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Theme != "system" && in.Theme != "light" && in.Theme != "dark" {
		s.fail(w, r, store.Invalid{Message: "ungültige Darstellung"})
		return
	}
	id := identity(r)
	_, err := audit.Exec(r.Context(), s.Pool, "INSERT INTO core.user_preferences(household_id,user_id,theme,offline_storage_enabled) VALUES($1,$2,$3,$4) ON CONFLICT(household_id,user_id) DO UPDATE SET theme=excluded.theme,offline_storage_enabled=excluded.offline_storage_enabled,updated_at=now()", id.HouseholdID, id.UserID, in.Theme, in.Offline)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}

func (s *Server) searchRows(r *http.Request) ([]store.Record, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 100 {
		return nil, store.Invalid{Message: "Suchbegriff zu lang"}
	}
	if q == "" {
		return []store.Record{}, nil
	}
	rows, err := s.Pool.Query(r.Context(), `SELECT d.module_id,d.resource_type,d.resource_id,d.title FROM core.search_documents d JOIN core.module_states m ON m.household_id=d.household_id AND m.module_id=d.module_id WHERE d.household_id=$1 AND m.state='enabled' AND m.data_generation=d.module_generation AND (d.search_vector @@ websearch_to_tsquery('german',$2) OR d.title ILIKE $3 OR d.title OPERATOR(core.%) $2) ORDER BY (ts_rank(d.search_vector,websearch_to_tsquery('german',$2))+core.similarity(d.title,$2)) DESC,d.title LIMIT 50`, identity(r).HouseholdID, q, "%"+q+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []store.Record{}
	for rows.Next() {
		var mid, entity, id, title string
		if err = rows.Scan(&mid, &entity, &id, &title); err != nil {
			return nil, err
		}
		if _, known := s.Store.Module(mid); known {
			list = append(list, store.Record{"module": mid, "entity": entity, "id": id, "title": title, "url": "/m/" + mid + "/" + entity + "/" + id})
		}
	}
	return list, rows.Err()
}
func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.searchRows(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Suche")
	p.View = "search"
	p.Query = r.URL.Query().Get("q")
	p.Records = rows
	s.render(w, r, p)
}
func (s *Server) searchAPI(w http.ResponseWriter, r *http.Request) {
	rows, err := s.searchRows(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, rows)
}
func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Mitteilungen")
	p.View = "notifications"
	rows, err := s.Pool.Query(r.Context(), "SELECT id,title,coalesce(target_path,''),read_at IS NOT NULL FROM core.notifications n WHERE user_id=$1 AND dismissed_at IS NULL AND (module_id IS NULL OR EXISTS(SELECT 1 FROM core.module_states m WHERE m.household_id=n.household_id AND m.module_id=n.module_id AND m.state='enabled')) ORDER BY created_at DESC LIMIT 100", identity(r).UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, target string
		var read bool
		if err = rows.Scan(&id, &title, &target, &read); err != nil {
			s.fail(w, r, err)
			return
		}
		p.Records = append(p.Records, store.Record{"id": id, "title": title, "url": target, "read": read})
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, p)
}
func (s *Server) readNotification(w http.ResponseWriter, r *http.Request) {
	if !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	_, err := audit.Exec(r.Context(), s.Pool, "UPDATE core.notifications SET read_at=coalesce(read_at,now()) WHERE id=$1 AND user_id=$2", r.PathValue("id"), identity(r).UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

func (s *Server) requestPurge(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	mid := r.PathValue("module")
	if id.Role != "admin" || !id.Recent() {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	if _, ok := s.Store.Module(mid); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if r.FormValue("confirm") != mid || r.FormValue("backup_reference") == "" {
		s.fail(w, r, store.Invalid{Message: "Modulkennung und geprüften Wiederherstellungspunkt angeben"})
		return
	}
	states := s.Store.States()
	if states[mid].State != "disabled" {
		s.fail(w, r, store.ErrConflict)
		return
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if err = auth.LockSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO core.module_purge_requests(household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,$3,$4,$5)", id.HouseholdID, mid, states[mid].Generation, id.UserID, r.FormValue("backup_reference"))
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Löschung angefordert")
	p.View = "message"
	p.Success = "Wartungsanforderung gespeichert. Der Betreiber führt die geprüfte Löschung mit dem Wartungsbefehl aus."
	s.render(w, r, p)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("module") == "pantry" {
		s.pantryStock(w, r)
		return
	}
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, ok := s.Store.Module(r.PathValue("module"))
	if !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, ok = s.Store.State(m.ID); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	result := map[string]any{}
	switch m.ID {
	case "expenses":
		rows, err := s.Pool.Query(r.Context(), "SELECT to_char(occurred_on,'YYYY-MM'),sum(amount_minor)::bigint FROM expenses.entries WHERE household_id=$1 AND state='posted' GROUP BY 1 ORDER BY 1 DESC LIMIT 24", identity(r).HouseholdID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer rows.Close()
		months := []store.Record{}
		for rows.Next() {
			var month string
			var amount int64
			if err = rows.Scan(&month, &amount); err != nil {
				s.fail(w, r, err)
				return
			}
			months = append(months, store.Record{"month": month, "amount_minor": amount})
		}
		if err = rows.Err(); err != nil {
			s.fail(w, r, err)
			return
		}
		result["months"] = months
	case "meters":
		readings, err := s.Store.MeterConsumption(r.Context(), identity(r))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		result["consumption"] = readings
	case "fuel":
		rows, err := s.Pool.Query(r.Context(), `SELECT f.id,f.vehicle_id,f.odometer_series_id,f.occurred_on,f.odometer::text,f.quantity::text,f.full_tank,f.previous_fill_missing,f.fuel_type,f.total_minor,v.name,s.reason,s.started_on FROM fuel.fillups f JOIN fuel.vehicles v ON v.household_id=f.household_id AND v.id=f.vehicle_id JOIN fuel.odometer_series s ON s.household_id=f.household_id AND s.id=f.odometer_series_id WHERE f.household_id=$1 ORDER BY f.vehicle_id,f.odometer_series_id,f.occurred_on,f.odometer,f.id LIMIT $2`, identity(r).HouseholdID, store.ReportHistoryLimit+1)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer rows.Close()
		list := []store.Record{}
		for rows.Next() {
			if len(list) == store.ReportHistoryLimit {
				s.fail(w, r, store.Invalid{Message: "Diese Auswertung unterstützt höchstens 10.000 Tankvorgänge. Es werden keine unvollständigen Ergebnisse angezeigt; für den vollständigen Datenbestand die Betreiber-Sicherung verwenden."})
				return
			}
			var rid, vehicle, series, odo, quantity, ft, vehicleName, reason string
			var day, seriesStart time.Time
			var full, missing bool
			var amount int64
			if err = rows.Scan(&rid, &vehicle, &series, &day, &odo, &quantity, &full, &missing, &ft, &amount, &vehicleName, &reason, &seriesStart); err != nil {
				s.fail(w, r, err)
				return
			}
			list = append(list, store.Record{"id": rid, "vehicle": vehicle, "series": series, "vehicle_name": vehicleName, "series_label": reason + " · " + seriesStart.Format("02.01.2006"), "date": day.Format("2006-01-02"), "odometer": odo, "quantity": quantity, "full": full, "missing": missing, "fuel_type": ft, "total_minor": amount})
		}
		if err = rows.Err(); err != nil {
			s.fail(w, r, err)
			return
		}
		result["fillups"] = list
		result["consumption"] = store.FuelConsumption(list)
	default:
		result["message"] = "Keine aggregierte Auswertung für dieses Modul"
	}
	writeJSON(w, 200, result)
}

var _ = errors.Is
var _ = fmt.Sprint
