package web

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"household/internal/store"
	"household/internal/value"
)

var fuelModulePanes = []string{"overview", "capture", "history", "reports", "costs", "maintenance", "charging", "trips", "imports", "documents", "odometer", "settings"}

func (s *Server) fuelModulePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	pane := r.PathValue("pane")
	if !slices.Contains(fuelModulePanes, pane) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, enabled := s.Store.State("fuel"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	// The workspace performs the same current-authority read as its JSON route.
	// No page shell is an authorization substitute for a later command.
	initial, err := s.Store.FuelModuleWorkspace(r.Context(), identity(r), store.FuelModuleQuery{Pane: "overview", VehicleID: r.URL.Query().Get("vehicle"), Limit: 1})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Fahrzeuge & Tanken")
	p.Module, _ = s.Store.Module("fuel")
	p.View = "fuel-module"
	p.Record = store.Record{"pane": pane, "vehicle": r.URL.Query().Get("vehicle"), "auth_scope": initial.Context.AuthScope, "generation": initial.Context.Generation}
	s.render(w, r, p)
}

func fuelModuleParameters(values url.Values) (store.FuelModuleQuery, error) {
	var q store.FuelModuleQuery
	allowed := []string{"pane", "vehicle", "job", "from", "to", "q", "cursor", "archived", "unit", "kind", "limit"}
	for key, entries := range values {
		if !slices.Contains(allowed, key) || len(entries) != 1 {
			return q, store.Invalid{Message: "Unbekannte oder doppelte Fahrzeugauswahl"}
		}
	}
	q = store.FuelModuleQuery{Pane: values.Get("pane"), VehicleID: values.Get("vehicle"), JobID: values.Get("job"), From: values.Get("from"), To: values.Get("to"), Search: values.Get("q"), Cursor: values.Get("cursor"), Archived: values.Get("archived") == "1", Unit: values.Get("unit"), Kind: values.Get("kind"), Limit: 50}
	if !slices.Contains(fuelModulePanes, q.Pane) || q.VehicleID != "" && !value.ValidUUID(q.VehicleID) || q.JobID != "" && !value.ValidUUID(q.JobID) || len(q.Search) > 300 || len(q.Cursor) > 16000 || len(q.Unit) > 30 || len(q.Kind) > 40 {
		return q, store.Invalid{Message: "Fahrzeugfilter erneut prüfen"}
	}
	if raw := values.Get("archived"); raw != "" && raw != "0" && raw != "1" {
		return q, store.Invalid{Message: "Archivauswahl erneut prüfen"}
	}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return q, store.Invalid{Message: "Seitengröße auf 1 bis 100 begrenzen"}
		}
		q.Limit = n
	}
	return q, nil
}

func (s *Server) fuelModuleWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if len(r.URL.RawQuery) > 20000 {
		s.fail(w, r, store.Invalid{Message: "Fahrzeugfilter zu lang"})
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "Fahrzeugfilter erneut prüfen"})
		return
	}
	q, err := fuelModuleParameters(values)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	binding := fuelModuleCursorScope(r, q)
	cursor, err := s.fuelModuleReadCursor(q.Cursor, binding)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q.Cursor = cursor.Raw
	out, err := s.Store.FuelModuleWorkspace(r.Context(), identity(r), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if out.Next != "" {
		expires := cursor.Expires
		if expires == 0 {
			expires = time.Now().Add(30 * time.Minute).Unix()
		}
		raw, encodeErr := json.Marshal(fuelModuleCursor{Version: 1, Expires: expires, Raw: out.Next})
		if encodeErr != nil || len(raw) > 11000 {
			s.fail(w, r, store.Invalid{Message: "Verlaufsseite zu groß. Auswahl enger begrenzen"})
			return
		}
		payload := base64.RawURLEncoding.EncodeToString(raw)
		out.Next = payload + "." + s.Auth.CSRF(payload, "fuel-module-page:"+binding)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fuelModuleRecordAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if r.URL.RawQuery != "" {
		s.fail(w, r, store.Invalid{Message: "Datensatz ohne zusätzliche Filter öffnen"})
		return
	}
	out, err := s.Store.FuelModuleRecord(r.Context(), identity(r), r.PathValue("entity"), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type fuelModuleReviewEnvelope struct {
	Version int                     `json:"v"`
	Key     string                  `json:"key"`
	Expires int64                   `json:"expires"`
	Command store.FuelModuleCommand `json:"command"`
}

func readFuelModuleJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return store.Invalid{Message: "Fahrzeugdaten sind ungültig oder überschreiten die erlaubte Größe"}
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return store.Invalid{Message: "Nur ein abgeschlossenes JSON-Dokument senden"}
	}
	return nil
}

type fuelModuleCursor struct {
	Version int    `json:"v"`
	Expires int64  `json:"expires"`
	Raw     string `json:"raw"`
}

func fuelModuleCursorScope(r *http.Request, query store.FuelModuleQuery) string {
	query.Cursor = ""
	raw, _ := json.Marshal([]any{fuelModuleReviewScope(r), query})
	return string(raw)
}

func (s *Server) fuelModuleReadCursor(token, scope string) (fuelModuleCursor, error) {
	var cursor fuelModuleCursor
	if token == "" {
		return cursor, nil
	}
	invalid := store.Invalid{Message: "Verlaufsseite ist abgelaufen oder gehört zu einer anderen Auswahl. Neu laden"}
	if len(token) > 16000 {
		return cursor, invalid
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || !s.Auth.ValidCSRF(payload, "fuel-module-page:"+scope, signature) {
		return cursor, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return cursor, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&cursor) != nil || d.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Expires <= time.Now().Unix() || cursor.Raw == "" || len(cursor.Raw) > 10000 {
		return fuelModuleCursor{}, invalid
	}
	return cursor, nil
}

func fuelModuleReviewScope(r *http.Request) string {
	id := identity(r)
	raw, _ := json.Marshal([]any{"fuel-module-review-v1", id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, id.Role})
	return string(raw)
}

func (s *Server) fuelModulePreviewAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	key := r.Header.Get("Idempotency-Key")
	if !value.ValidUUID(key) {
		s.fail(w, r, store.Invalid{Message: "Vorgangsschlüssel fehlt. Auswahl erneut prüfen"})
		return
	}
	var command store.FuelModuleCommand
	if err := readFuelModuleJSON(w, r, &command, 4<<20); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Store.FuelModulePreview(r.Context(), identity(r), command)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if out.Command.SourceStamp == "" {
		out.Command.SourceStamp = out.Context.Stamp
	}
	expires := time.Now().Add(10 * time.Minute).Unix()
	raw, err := json.Marshal(fuelModuleReviewEnvelope{Version: 1, Key: key, Expires: expires, Command: out.Command})
	if err != nil || len(raw) > 4<<20 {
		s.fail(w, r, store.Invalid{Message: "Prüfvorschlag zu groß. Auswahl verkleinern"})
		return
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	token := payload + "." + s.Auth.CSRF(payload, fuelModuleReviewScope(r))
	writeJSON(w, http.StatusOK, struct {
		store.FuelModulePreview
		Review  string `json:"review"`
		Expires int64  `json:"expires"`
	}{out, token, expires})
}

func (s *Server) fuelModuleExecuteAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	var input struct {
		Review string `json:"review"`
	}
	if err := readFuelModuleJSON(w, r, &input, 6<<20); err != nil {
		s.fail(w, r, err)
		return
	}
	invalid := store.Invalid{Message: "Prüfvorschlag ist ungültig oder abgelaufen. Verlauf prüfen und die unveränderte Auswahl erneut prüfen"}
	if len(input.Review) > 5800000 {
		s.fail(w, r, invalid)
		return
	}
	payload, signature, ok := strings.Cut(input.Review, ".")
	if !ok || !s.Auth.ValidCSRF(payload, fuelModuleReviewScope(r), signature) {
		s.fail(w, r, invalid)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		s.fail(w, r, invalid)
		return
	}
	var envelope fuelModuleReviewEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || envelope.Version != 1 || envelope.Expires <= time.Now().Unix() || !value.ValidUUID(envelope.Key) || envelope.Key != r.Header.Get("Idempotency-Key") {
		s.fail(w, r, invalid)
		return
	}
	out, err := s.Store.FuelModuleExecute(r.Context(), identity(r), envelope.Key, trace(r), envelope.Command)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fuelModuleOperationStatusAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	key := r.PathValue("key")
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !value.ValidUUID(key) || len(values) != 1 || len(values["generation"]) != 1 {
		s.fail(w, r, store.Invalid{Message: "Vorgangsbeleg erneut prüfen"})
		return
	}
	generation, err := strconv.ParseInt(values.Get("generation"), 10, 64)
	if err != nil || generation <= 0 {
		s.fail(w, r, store.Invalid{Message: "Datenstand des Vorgangs fehlt"})
		return
	}
	out, err := s.Store.FuelModuleOperationStatus(r.Context(), identity(r), key, generation)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fuelModuleAbandonAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	key := r.PathValue("key")
	var input struct {
		Generation int64 `json:"generation"`
	}
	if !value.ValidUUID(key) {
		s.fail(w, r, store.Invalid{Message: "Vorgangsschlüssel erneut prüfen"})
		return
	}
	if err := readFuelModuleJSON(w, r, &input, 1024); err != nil {
		s.fail(w, r, err)
		return
	}
	if input.Generation <= 0 {
		s.fail(w, r, store.Invalid{Message: "Datenstand des Vorgangs fehlt"})
		return
	}
	out, err := s.Store.FuelModuleAbandonOperation(r.Context(), identity(r), key, input.Generation, trace(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fuelModuleExportAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if len(r.URL.RawQuery) > 20000 {
		s.fail(w, r, store.Invalid{Message: "Exportauswahl zu lang"})
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "Exportauswahl erneut prüfen"})
		return
	}
	q, err := fuelModuleParameters(values)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if q.Cursor != "" {
		s.fail(w, r, store.Invalid{Message: "Export aus einer vollständigen Auswahl statt einer Folgeseite starten"})
		return
	}
	header, rows, err := s.Store.FuelModuleExport(r.Context(), identity(r), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// CSV is a spreadsheet download. Prefix formula-leading text; precise
	// source strings remain otherwise unchanged and are never float-parsed.
	var data bytes.Buffer
	writer := csv.NewWriter(&data)
	for _, row := range append([][]string{header}, rows...) {
		clean := append([]string(nil), row...)
		for i, cell := range clean {
			trimmed := strings.TrimLeft(cell, " \t\r\n")
			if strings.HasPrefix(cell, "\t") || strings.HasPrefix(cell, "\r") || strings.HasPrefix(cell, "\n") || trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
				clean[i] = "'" + cell
			}
		}
		if err = writer.Write(clean); err != nil {
			s.fail(w, r, err)
			return
		}
		if data.Len() > 32<<20 {
			s.fail(w, r, store.Invalid{Message: "CSV-Ausgabe zu groß. Datumsfenster verkleinern"})
			return
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		s.fail(w, r, err)
		return
	}
	if data.Len() > 32<<20 {
		s.fail(w, r, store.Invalid{Message: "CSV-Ausgabe zu groß. Datumsfenster verkleinern"})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="household-fahrzeuge.csv"`)
	_, _ = w.Write(data.Bytes())
}
