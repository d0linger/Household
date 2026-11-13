package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/i18n"
	"household/internal/module"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

const localeKey requestKey = 4

func localizedTemplateFuncs(locale string) template.FuncMap {
	return template.FuncMap{
		"t":      func(source string, args ...any) string { return i18n.Text(locale, source, args...) },
		"choice": func(value any) string { return i18n.Text(locale, formatChoice(value)) },
		"qty":    func(value any) string { return localizedNumeric(locale, formatQuantity(value)) },
		"money":  func(value any) string { return localizedNumeric(locale, formatMoney(value)) },
	}
}

// Presentation only: preserve the existing exact decimal/integer formatter.
func localizedNumeric(locale, formatted string) string {
	if locale != "en" {
		return formatted
	}
	return strings.NewReplacer(".", ",", ",", ".").Replace(formatted)
}

func requestLocale(r *http.Request) string {
	if locale, ok := r.Context().Value(localeKey).(string); ok {
		return i18n.Normalize(locale)
	}
	if cookie, err := r.Cookie("household_locale"); err == nil && validLocale(cookie.Value) {
		return cookie.Value
	}
	return "de"
}

func validLocale(locale string) bool { return locale == "de" || locale == "en" }

func (s *Server) accountLocale(r *http.Request, id auth.Identity) (*http.Request, error) {
	locale := requestLocale(r)
	var saved *string
	var order []string
	err := s.Pool.QueryRow(r.Context(), `SELECT locale,module_order FROM core.user_preferences
 WHERE household_id=$1 AND user_id=$2`, id.HouseholdID, id.UserID).Scan(&saved, &order)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return r, err
	}
	if saved != nil && validLocale(*saved) {
		locale = *saved
	}
	ctx := context.WithValue(r.Context(), localeKey, locale)
	return r.WithContext(context.WithValue(ctx, moduleOrderKey, order)), nil
}

func localizedEntity(locale string, entity module.Entity) module.Entity {
	entity.Title = i18n.Text(locale, entity.Title)
	entity.Fields = append([]module.Field(nil), entity.Fields...)
	for index := range entity.Fields {
		entity.Fields[index].Label = i18n.Text(locale, entity.Fields[index].Label)
	}
	return entity
}

func localizedModule(locale string, descriptor module.Descriptor) module.Descriptor {
	descriptor.Title = i18n.Text(locale, descriptor.Title)
	descriptor.Short = i18n.Text(locale, descriptor.Short)
	descriptor.Entities = append([]module.Entity(nil), descriptor.Entities...)
	for index, entity := range descriptor.Entities {
		descriptor.Entities[index] = localizedEntity(locale, entity)
	}
	return descriptor
}

func localizedModules(locale string, modules []module.Descriptor) []module.Descriptor {
	result := make([]module.Descriptor, len(modules))
	for index, descriptor := range modules {
		result[index] = localizedModule(locale, descriptor)
	}
	return result
}

// Generic form validation owns both the field label and its suffix. Resolve
// only those closed errors; arbitrary captured values in other errors remain
// unchanged, even when their spelling happens to match application copy.
func (s *Server) localizedError(locale, message string) string {
	if locale != "en" || s.Store == nil {
		return i18n.Message(locale, message)
	}
	for _, suffix := range []string{"erforderlich", "ungültiger Wert", "ungültiger Schalter", "Text erforderlich", "zu lang", "ungültiger Text", "unbekannte Auswahl", "ungültige Referenz", "ungültiges Datum", "Datum mit Zeitzone erforderlich", "gültige Menge erforderlich", "ungültige ganze Zahl", "gültiger Betrag erforderlich"} {
		ending := ": " + suffix
		if !strings.HasSuffix(message, ending) {
			continue
		}
		label := strings.TrimSuffix(message, ending)
		for _, descriptor := range s.Store.Modules {
			for _, entity := range descriptor.Entities {
				for _, field := range entity.Fields {
					if field.Label == label {
						return i18n.Text(locale, "%s"+ending, i18n.Text(locale, label))
					}
				}
			}
		}
	}
	return i18n.Message(locale, message)
}

func (s *Server) localeCookie(w http.ResponseWriter, locale string) {
	http.SetCookie(w, s.cookie("household_locale", locale, 365*24*60*60, time.Time{}))
}

func languageReturn(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 2048 || parsed.IsAbs() || parsed.Host != "" ||
		!strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") ||
		strings.ContainsAny(parsed.Path, "\\\r\n") {
		return "/settings"
	}
	return parsed.RequestURI()
}

func (s *Server) language(w http.ResponseWriter, r *http.Request) {
	locale := r.FormValue("locale")
	if !validLocale(locale) {
		s.fail(w, r, store.Invalid{Message: "Ungültige Sprache"})
		return
	}
	id := identity(r)
	if id.UserID == "" {
		challenge, err := r.Cookie("household_login")
		if err != nil || !s.Auth.ValidCSRF(challenge.Value, "login", r.FormValue("csrf")) {
			s.fail(w, r, store.ErrForbidden)
			return
		}
	} else {
		_, err := audit.Exec(r.Context(), s.Pool, `INSERT INTO core.user_preferences(household_id,user_id,locale)
 VALUES($1,$2,$3) ON CONFLICT(household_id,user_id) DO UPDATE SET locale=excluded.locale,updated_at=now()`,
			id.HouseholdID, id.UserID, locale)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.localeCookie(w, locale)
	target := "/login"
	if id.UserID != "" {
		target = languageReturn(r.FormValue("return_to"))
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) getPreferences(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	theme, locale, offline := "system", requestLocale(r), false
	order := []string{}
	err := s.Pool.QueryRow(r.Context(), `SELECT theme,coalesce(locale,$3),offline_storage_enabled,module_order
 FROM core.user_preferences WHERE household_id=$1 AND user_id=$2`,
		id.HouseholdID, id.UserID, locale).Scan(&theme, &locale, &offline, &order)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"theme": theme, "locale": locale, "offline": offline, "module_order": order,
		"effective_module_order": moduleIDs(orderedModules(s.Store.Modules, order))})
}
