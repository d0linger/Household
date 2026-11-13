package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"

	"household/internal/store"
	"household/internal/value"
)

var energyPanes = []string{"overview", "capture", "history", "costs", "plans", "imports", "settings"}

func (s *Server) registerEnergyRoutes(mux *http.ServeMux) {
	mux.Handle("GET /m/meters/workspace/{pane}", s.require(s.energyPage))
	mux.Handle("GET /api/v1/meters/{view}", s.require(s.energyAPI))
	mux.Handle("POST /api/v1/meters/commands", s.require(s.energyAPI))
	mux.Handle("POST /api/v1/meters/readings/{id}/photo", s.require(s.energyPhoto))
}

func (s *Server) energyPhoto(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	r.SetPathValue("module", "meters")
	r.SetPathValue("entity", "readings")
	s.upload(w, r)
}

func (s *Server) energyPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if !slices.Contains(energyPanes, r.PathValue("pane")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	query, err := energyQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	initial, err := s.Store.EnergyContext(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Zähler & Energie")
	p.Module, _ = s.Store.Module("meters")
	p.View = "meters-workspace"
	p.Extra = store.Record{"pane": r.PathValue("pane"), "register_id": query.Get("register"), "generation": initial["generation"],
		"ha_available": s.Cfg.MeterFeeds.HAURL != "", "mqtt_available": s.Cfg.MeterFeeds.MQTTURL != ""}
	s.render(w, r, p)
}

func energyQuery(r *http.Request) (url.Values, error) {
	if len(r.URL.RawQuery) > 5000 {
		return nil, store.Invalid{Message: "Zählerfilter ist zu groß"}
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, store.Invalid{Message: "Ungültiger Zählerfilter"}
	}
	for name, values := range query {
		if !slices.Contains([]string{"register", "from", "through", "cursor", "grain", "search"}, name) || len(values) != 1 {
			return nil, store.Invalid{Message: "Unbekannter oder mehrfacher Zählerfilter"}
		}
	}
	if query.Get("register") != "" && !value.ValidUUID(query.Get("register")) || len(query.Get("search")) > 200 || len(query.Get("cursor")) > 3000 {
		return nil, store.Invalid{Message: "Register, Suchtext oder Messwertseite prüfen"}
	}
	return query, nil
}

func readEnergyJSON(w http.ResponseWriter, r *http.Request, into *store.EnergyCommand) error {
	invalid := store.Invalid{Message: "Zählervorgang ist unvollständig, mehrdeutig oder zu groß"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return invalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 10000
	if planningJSONValue(decoder, reflect.TypeOf(into), 0, &remaining) != nil {
		return invalid
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return invalid
	}
	for _, required := range []string{"generation", "version", "action", "id", "data"} {
		if _, exists := members[required]; !exists {
			return invalid
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

func (s *Server) energyAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	var result store.Record
	var err error
	if r.Method == http.MethodPost {
		var in store.EnergyCommand
		err = readEnergyJSON(w, r, &in)
		if err == nil {
			result, err = s.Store.EnergyWrite(r.Context(), identity(r), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	} else {
		q, parseErr := energyQuery(r)
		if parseErr != nil {
			s.fail(w, r, parseErr)
			return
		}
		query := store.EnergyQuery{RegisterID: q.Get("register"), From: q.Get("from"), Through: q.Get("through"),
			Cursor: q.Get("cursor"), Grain: q.Get("grain"), Search: q.Get("search")}
		switch view := r.PathValue("view"); view {
		case "notification-settings":
			result, err = s.Store.MeterNotificationSettings(r.Context(), identity(r), query.RegisterID)
		case "overview":
			result, err = s.Store.EnergyOverview(r.Context(), identity(r), query.Search)
		case "context":
			result, err = s.Store.EnergyContext(r.Context(), identity(r))
		case "history":
			result, err = s.Store.EnergyHistory(r.Context(), identity(r), query)
		case "series":
			if query.Grain == "" {
				query.Grain = "day"
			}
			result, err = s.Store.EnergySeries(r.Context(), identity(r), query)
		case "devices", "tariffs", "payments", "bills", "plans", "sources", "changes":
			result, err = s.Store.EnergyManagement(r.Context(), identity(r), view, query.RegisterID, query)
		default:
			err = store.ErrNotFound
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
