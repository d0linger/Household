package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type referenceOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type referencePage struct {
	Records []referenceOption `json:"records"`
	Next    string            `json:"next_cursor"`
}

type referenceCursor struct {
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
	Expires int64     `json:"expires"`
	Rank    int64     `json:"rank,omitempty"`
	Group   string    `json:"group,omitempty"`
	Sort    int64     `json:"sort,omitempty"`
	Name    string    `json:"name,omitempty"`
}

// referenceOptions exposes only labels and IDs. Immutable creation coordinates
// keep paging independent of edited names; newly created records appear on reload.
func (s *Server) referenceOptions(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	mid, entity := r.PathValue("module"), r.PathValue("entity")
	favorites := r.URL.Query().Get("favorites") == "1"
	if favorites && (mid != "shopping" || entity != "products") {
		s.fail(w, r, store.Invalid{Message: "Favoriten sind nur für Einkaufsprodukte verfügbar"})
		return
	}
	selectedID := r.PathValue("id")
	var selected any
	if selectedID != "" {
		if !value.ValidUUID(selectedID) {
			s.fail(w, r, store.ErrNotFound)
			return
		}
		selected = selectedID
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 300 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		s.fail(w, r, store.Invalid{Message: "Suchbegriff ist ungültig oder zu lang"})
		return
	}
	from, label, condition := "", "", ""
	generation := int64(0)
	if mid == "core" && entity == "memberships" {
		from = "core.memberships t JOIN core.users u ON u.id=t.user_id"
		label = "u.display_name||CASE WHEN t.inactive_at IS NOT NULL OR u.disabled_at IS NOT NULL THEN ' (nicht mehr aktiv)' ELSE '' END"
		condition = " AND ($5::uuid IS NOT NULL OR t.inactive_at IS NULL AND u.disabled_at IS NULL)"
	} else {
		m, e, err := s.resolve(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		from = pgx.Identifier{m.ID, e.ID}.Sanitize() + " t"
		label = "t." + pgx.Identifier{e.Primary}.Sanitize() + "::text"
		st, _ := s.Store.State(mid)
		generation = st.Generation
		if mid == "shopping" && entity == "products" {
			label = "t.name||CASE WHEN t.brand<>'' THEN ' · '||t.brand ELSE '' END||' · '||trim_scale(t.pack_quantity)::text||' '||CASE WHEN t.pack_unit='piece' THEN 'Stück' ELSE t.pack_unit END"
			if favorites {
				condition = " AND t.favorite"
			}
		}
		if mid == "shopping" && entity == "lists" {
			condition = " AND t.archived_at IS NULL AND ($5::uuid IS NOT NULL OR NOT t.is_template)"
		}
		if mid == "shopping" && entity == "items" {
			condition = " AND EXISTS(SELECT 1 FROM shopping.lists l WHERE l.household_id=t.household_id AND l.id=t.list_id AND l.archived_at IS NULL)"
		}
	}
	scopeBytes, err := json.Marshal([]any{identity(r).HouseholdID, identity(r).UserID, mid, entity, generation, query, selectedID, favorites})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	scope := string(scopeBytes)
	cursor, err := s.readReferenceCursor(r.URL.Query().Get("cursor"), scope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var created, lastID any
	if cursor.ID != "" {
		created, lastID = cursor.Created, cursor.ID
	}
	// Table and column expressions above are compiled descriptors, never request text.
	rows, err := s.Pool.Query(r.Context(), "SELECT t.id,COALESCE("+label+",''),t.created_at FROM "+from+
		" WHERE t.household_id=$1 AND ($5::uuid IS NULL OR t.id=$5) AND ($2='' OR strpos(lower(COALESCE("+label+",'')),lower($2))>0)"+condition+
		" AND ($3::timestamptz IS NULL OR (t.created_at,t.id)<($3,$4::uuid)) ORDER BY t.created_at DESC,t.id DESC LIMIT 51",
		identity(r).HouseholdID, query, created, lastID, selected)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	page := referencePage{Records: []referenceOption{}}
	var last referenceCursor
	for rows.Next() {
		var option referenceOption
		var at time.Time
		if err = rows.Scan(&option.ID, &option.Label, &at); err != nil {
			s.fail(w, r, err)
			return
		}
		if len(page.Records) == 50 {
			page.Next, err = s.makeReferenceCursor(last, scope)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			break
		}
		if option.Label == "" {
			option.Label = "Eintrag · " + option.ID[:8]
		}
		page.Records = append(page.Records, option)
		last = referenceCursor{Created: at, ID: option.ID, Expires: time.Now().Add(30 * time.Minute).Unix()}
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) makeReferenceCursor(cursor referenceCursor, scope string) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + s.Auth.CSRF(payload, "reference-page:"+scope), nil
}

func (s *Server) readReferenceCursor(token, scope string) (referenceCursor, error) {
	var cursor referenceCursor
	if token == "" {
		return cursor, nil
	}
	invalid := store.Invalid{Message: "Auswahlseite ist abgelaufen oder ungültig. Suche erneut starten."}
	// A board cursor contains up to 500 name/group runes. JSON may escape each
	// rune as six bytes before base64 encoding, plus bounded keys and signature.
	if len(token) > 8192 {
		return cursor, invalid
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || !s.Auth.ValidCSRF(payload, "reference-page:"+scope, signature) {
		return cursor, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &cursor) != nil || !value.ValidUUID(cursor.ID) || cursor.Created.IsZero() || cursor.Expires <= time.Now().Unix() {
		return referenceCursor{}, invalid
	}
	return cursor, nil
}
