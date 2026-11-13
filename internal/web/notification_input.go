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

type notificationPreferenceInput struct {
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
	Timezone string `json:"timezone"`
	Start    *int   `json:"quiet_start"`
	End      *int   `json:"quiet_end"`
	Version  int64  `json:"version"`
}

// All six keys are required; only the two optional quiet-hour values may be
// null. Check decoded key tokens before typed decoding so duplicate or aliased
// keys cannot silently override a user's choice.
func readNotificationPreferenceJSON(w http.ResponseWriter, r *http.Request, into *notificationPreferenceInput) error {
	invalid := store.Invalid{Message: "Ungültige Benachrichtigungseinstellung"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	raw, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(raw) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid
	}
	keys := make(map[string]bool, 6)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || keys[key] {
			return invalid
		}
		nullable := key == "quiet_start" || key == "quiet_end"
		if !nullable && key != "category" && key != "enabled" && key != "timezone" && key != "version" {
			return invalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || !nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid
		}
		keys[key] = true
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(keys) != 6 {
		return invalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return invalid
	}
	if json.Unmarshal(raw, into) != nil {
		return invalid
	}
	return nil
}
