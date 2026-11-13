package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"household/internal/store"
)

func (s *Server) financePaymentReminderAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if r.URL.RawQuery != "" {
		s.fail(w, r, store.Invalid{Message: "Zahlungserinnerung ohne zusätzliche URL-Parameter öffnen"})
		return
	}
	out, err := s.Store.FinancePaymentReminder(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financePaymentReminderSaveAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	var in store.FinancePaymentReminderInput
	if err := readFinancePaymentReminderJSON(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Store.SaveFinancePaymentReminder(r.Context(), identity(r), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// The reminder command is a closed flat object. Requiring all fields, rejecting
// null and checking decoded keys prevents duplicate or case-aliased overrides
// before encoding/json's permissive struct matching can take effect.
func readFinancePaymentReminderJSON(w http.ResponseWriter, r *http.Request, into *store.FinancePaymentReminderInput) error {
	invalid := store.Invalid{Message: "Ungültige Angaben zur Zahlungserinnerung"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	raw, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(raw) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid
	}
	keys := make(map[string]bool, 9)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || keys[key] {
			return invalid
		}
		switch key {
		case "action", "version", "entry_version", "generation", "access_version", "due_on", "responsible_member_id", "request_id":
		default:
			return invalid
		}
		var data json.RawMessage
		if decoder.Decode(&data) != nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return invalid
		}
		keys[key] = true
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(keys) != 8 {
		return invalid
	}
	if _, err = decoder.Token(); err != io.EOF || json.Unmarshal(raw, into) != nil {
		return invalid
	}
	return nil
}
