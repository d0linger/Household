package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"household/internal/store"
	"household/internal/webpush"
)

func (s *Server) shoppingExtensionsContextAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	current, err := s.Store.ShoppingExtensionsContext(ctx, identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	configuration := webpush.Configuration{State: "disabled", DeviceLimit: 5}
	if s.Push != nil {
		configuration = s.Push.Configuration()
	}
	writeJSON(w, 200, struct {
		Context store.ShoppingExtensionContext `json:"context"`
		Limits  map[string]int                 `json:"limits"`
		Push    shoppingPushConfiguration      `json:"push"`
	}{current, map[string]int{"offline_lists": 10, "offline_items": 1000, "offline_bytes": 2097152, "offline_days": 7, "notification_watches": 20, "push_devices": 5}, shoppingPushConfiguration{configuration.State, configuration.ApplicationServerKey, configuration.VAPIDKeyID}})
}

type shoppingPushConfiguration struct {
	State                string  `json:"state"`
	ApplicationServerKey *string `json:"application_server_key"`
	VAPIDKeyID           *string `json:"vapid_key_id"`
}

func (s *Server) shoppingNotificationPreferenceAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var view store.ShoppingPreferenceView
	var err error
	if r.Method == http.MethodGet {
		var generation int64
		generation, err = shoppingExtensionGeneration(r.URL.RawQuery)
		if err == nil {
			view, err = s.Store.ShoppingNotificationPreferences(ctx, identity(r), r.PathValue("id"), generation)
		}
	} else {
		var input store.ShoppingPreferenceInput
		err = readShoppingExtensionJSON(w, r, &input, 2048)
		if err == nil {
			view, err = s.Store.SetShoppingNotificationPreferences(ctx, identity(r), r.PathValue("id"), trace(r), input)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) shoppingNotificationOverviewAPI(w http.ResponseWriter, r *http.Request) {
	generation, err := shoppingExtensionGeneration(r.URL.RawQuery)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	view, err := s.Store.ShoppingNotificationOverview(ctx, identity(r), generation)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) shoppingOfflineSnapshotAPI(w http.ResponseWriter, r *http.Request) {
	var input store.ShoppingSnapshotInput
	if err := readShoppingExtensionJSON(w, r, &input, 2048); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	snapshot, err := s.Store.ShoppingOfflineSnapshots(ctx, identity(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, snapshot)
}

func (s *Server) pushContextAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	view, err := s.Push.Context(ctx, identity(r))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) pushSubscriptionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if r.Method == http.MethodGet {
		view, err := s.Push.Subscriptions(ctx, identity(r))
		if err != nil {
			s.pushAPIError(w, r, err)
			return
		}
		writeJSON(w, 200, view)
		return
	}
	var input webpush.RegisterInput
	if err := readShoppingExtensionJSON(w, r, &input, 8192); err != nil {
		s.fail(w, r, err)
		return
	}
	view, err := s.Push.Register(ctx, identity(r), input)
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) pushDisableAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	view, err := s.Push.Disable(ctx, identity(r), r.PathValue("id"))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) pushAPIError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, webpush.ErrLimited) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, 429, map[string]string{"error": "Zu viele Geräteanfragen; bitte später erneut versuchen", "request_id": trace(r)})
		return
	}
	if errors.Is(err, webpush.ErrUnavailable) {
		writeJSON(w, 409, map[string]string{"error": "Push ist derzeit nicht eingerichtet oder deaktiviert", "request_id": trace(r)})
		return
	}
	s.fail(w, r, err)
}

func shoppingExtensionGeneration(rawQuery string) (int64, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil || len(query) != 1 || len(query["generation"]) != 1 {
		return 0, store.Invalid{Message: "Aktuellen Einkaufsdatenstand erneut laden"}
	}
	raw := query.Get("generation")
	generation, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || generation < 1 || generation > 9007199254740991 || strconv.FormatInt(generation, 10) != raw {
		return 0, store.Invalid{Message: "Gültiger Einkaufsdatenstand erforderlich"}
	}
	return generation, nil
}

// A closed, bounded object also rejects duplicate keys before typed decoding.
func readShoppingExtensionJSON(w http.ResponseWriter, r *http.Request, into any, limit int64) error {
	invalid := store.Invalid{Message: "Einkaufs- oder Gerätedaten sind ungültig oder überschreiten die erlaubte Größe"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return invalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return invalid
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	if err = shoppingJSONValue(tokens, reflect.TypeOf(into), 0); err != nil {
		return invalid
	}
	if _, err = tokens.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

func shoppingJSONValue(decoder *json.Decoder, expected reflect.Type, depth int) error {
	if depth > 8 {
		return errors.New("JSON nesting limit")
	}
	for expected != nil && expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null is not an input value")
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	if expected == nil || delimiter == '{' && expected.Kind() != reflect.Struct || delimiter == '[' && expected.Kind() != reflect.Slice {
		return errors.New("unexpected JSON structure")
	}
	fields := make(map[string]reflect.Type)
	if delimiter == '{' {
		for index := 0; index < expected.NumField(); index++ {
			field := expected.Field(index)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.IsExported() && name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
	}
	keys := make(map[string]bool)
	for decoder.More() {
		var child reflect.Type
		if delimiter == '{' {
			key, keyErr := decoder.Token()
			name, valid := key.(string)
			child = fields[name]
			if keyErr != nil || !valid || keys[name] || child == nil {
				return errors.New("invalid or duplicate JSON key")
			}
			keys[name] = true
		} else {
			child = expected.Elem()
		}
		if err = shoppingJSONValue(decoder, child, depth+1); err != nil {
			return err
		}
	}
	if delimiter == '{' && len(keys) != len(fields) {
		return errors.New("required JSON member is missing")
	}
	_, err = decoder.Token()
	return err
}
