package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"reflect"
	"unicode/utf8"

	"household/internal/store"
)

func (s *Server) planningNotificationsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if r.URL.RawQuery != "" {
		s.fail(w, r, store.Invalid{Message: "Zubereitungshinweise ohne zusätzliche URL-Parameter öffnen"})
		return
	}
	var out store.Record
	var err error
	if r.Method == http.MethodGet {
		out, err = s.Store.PlanningNotificationSettings(r.Context(), identity(r), r.PathValue("plan"), r.PathValue("meal"))
	} else {
		var in store.PlanningNotificationInput
		err = readPlanningNotificationJSON(w, r, &in)
		if err == nil {
			out, err = s.Store.SavePlanningNotificationSettings(r.Context(), identity(r), r.PathValue("plan"),
				r.PathValue("meal"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func readPlanningNotificationJSON(w http.ResponseWriter, r *http.Request, into *store.PlanningNotificationInput) error {
	invalid := store.Invalid{Message: "Zubereitungshinweise sind unvollständig, ungültig oder zu groß"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if err != nil || !utf8.Valid(raw) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 200
	if planningJSONValue(decoder, reflect.TypeOf(into), 0, &remaining) != nil {
		return invalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return invalid
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) != 9 {
		return invalid
	}
	for _, name := range []string{"generation", "plan_version", "meal_version", "settings_version", "timezone", "responsible_member_id", "enabled", "confirmed", "steps"} {
		if _, ok := members[name]; !ok {
			return invalid
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil {
		return invalid
	}
	return nil
}
