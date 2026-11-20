package web

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/buildinfo"
	"household/internal/store"
)

func (s *Server) registerExportRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/core/export.zip", s.require(s.householdExport))
	mux.Handle("GET /api/v1/core/audit/export.jsonl", s.require(s.auditExport))
	mux.Handle("POST /api/v1/core/client-errors", s.require(s.clientError))
	mux.Handle("GET /help", s.require(s.helpPage))
	mux.Handle("GET /share", s.require(s.sharePage))
	mux.HandleFunc("POST /share", s.shareFallback)
}

// sharePage lets the user turn files shared from another app into documents.
// The service worker parks shared files on the device; nothing reaches the
// server until the user saves them through the normal upload routes.
func (s *Server) sharePage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Geteilte Dateien")
	p.View = "share"
	s.render(w, r, p)
}

// shareFallback answers a share that reached the server because the service
// worker was not active yet. It reads no body and changes nothing.
func (s *Server) shareFallback(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/share?missed=1", http.StatusSeeOther)
}

// householdExportBudget bounds the whole archive in memory before it is sent,
// so a failure can still answer with an error status.
const householdExportBudget = 96 << 20

// householdExport bundles every enabled module entity as CSV plus a manifest.
// Attachments stay out: the documents export and backups carry the files.
func (s *Server) householdExport(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	if !id.Recent() {
		s.fail(w, r, auth.ErrRecent)
		return
	}
	select {
	case exportSlots <- struct{}{}:
		defer func() { <-exportSlots }()
	default:
		w.Header().Set("Retry-After", "5")
		s.fail(w, r, store.ErrBusy)
		return
	}
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	type entry struct {
		Module string `json:"module"`
		Entity string `json:"entity"`
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	}
	manifest := struct {
		Format     string    `json:"format"`
		Release    string    `json:"release"`
		ExportedAt time.Time `json:"exported_at"`
		Entries    []entry   `json:"entries"`
		Omitted    []string  `json:"omitted"`
	}{Format: "household-export-1", Release: buildinfo.Label(), ExportedAt: time.Now().UTC(), Entries: []entry{}, Omitted: []string{}}
	var archive bytes.Buffer
	zipper := zip.NewWriter(&archive)
	states := s.Store.States()
	for _, m := range s.Store.Modules {
		if states[m.ID].State != "enabled" {
			continue
		}
		for _, e := range m.Entities {
			data, err := s.entityCSV(r.Context(), id, m, e, true)
			var invalid store.Invalid
			if errors.As(err, &invalid) || errors.Is(err, store.ErrForbidden) {
				// Too large for an export, or not visible to this administrator.
				manifest.Omitted = append(manifest.Omitted, m.ID+"/"+e.ID)
				continue
			}
			if err != nil {
				s.fail(w, r, err)
				return
			}
			name := m.ID + "/" + e.ID + ".csv"
			file, err := zipper.Create(name)
			if err == nil {
				_, err = file.Write(data)
			}
			if err != nil {
				s.fail(w, r, err)
				return
			}
			sum := sha256.Sum256(data)
			manifest.Entries = append(manifest.Entries, entry{Module: m.ID, Entity: e.ID, File: name, SHA256: hex.EncodeToString(sum[:]), Bytes: len(data)})
			if archive.Len() > householdExportBudget {
				s.fail(w, r, store.Invalid{Message: "Export überschreitet die unterstützte Ausgabegröße; Betreiber-Sicherung verwenden"})
				return
			}
		}
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		var file interface{ Write([]byte) (int, error) }
		if file, err = zipper.Create("manifest.json"); err == nil {
			_, err = file.Write(raw)
		}
	}
	if err == nil {
		err = zipper.Close()
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.recordExport(r, "households", "household_exported"); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Log.Info("household export", "request_id", trace(r), "entries", len(manifest.Entries), "omitted", len(manifest.Omitted))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="household-export-`+time.Now().Format("2006-01-02")+`.zip"`)
	s.writeBytes(w, r, archive.Bytes())
}

// recordExport writes the semantic audit event for a completed export before
// any byte is sent; without the event the copy is not handed out.
func (s *Server) recordExport(r *http.Request, entity, action string) error {
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if err = audit.Event(r.Context(), tx, "core", entity, "", action, nil); err != nil {
		return err
	}
	return tx.Commit(r.Context())
}

// auditChainSeed starts every export's hash chain.
const auditChainSeed = "household-audit-export-1"

// auditChainStep computes the next chain value from the previous one and
// the canonical JSON of the entry.
func auditChainStep(previous string, line []byte) string {
	h := sha256.New()
	h.Write([]byte(previous))
	h.Write([]byte{'\n'})
	h.Write(line)
	return hex.EncodeToString(h.Sum(nil))
}

// auditExportLimit bounds one export; narrow the filters for older history.
const auditExportLimit = 20000

// auditExport streams the filtered audit history as JSON lines, newest
// first. Every line carries a chained SHA-256, and a final manifest names the
// count and final chain value, so a stored copy can later be checked against
// any modification. The same visibility rules as the viewer apply.
func (s *Server) auditExport(w http.ResponseWriter, r *http.Request) {
	if identity(r).Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	select {
	case exportSlots <- struct{}{}:
		defer func() { <-exportSlots }()
	default:
		w.Header().Set("Retry-After", "5")
		s.fail(w, r, store.ErrBusy)
		return
	}
	// A supplied before cursor continues a truncated export; readAudit validates it.
	query := r.URL.Query()
	var lines bytes.Buffer
	chain := auditChainStep("", []byte(auditChainSeed))
	count, truncated := 0, false
	var last int64
	for {
		page := r.Clone(r.Context())
		page.URL = &url.URL{Path: r.URL.Path, RawQuery: query.Encode()}
		result, err := s.readAudit(page)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, item := range result.Entries {
			if count == auditExportLimit {
				truncated = true
				break
			}
			raw, err := json.Marshal(item)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			chain = auditChainStep(chain, raw)
			line, _ := json.Marshal(struct {
				Entry json.RawMessage `json:"entry"`
				Chain string          `json:"chain"`
			}{raw, chain})
			lines.Write(line)
			lines.WriteByte('\n')
			count++
			last = item.ID
		}
		if truncated || result.Next == 0 {
			break
		}
		query.Set("before", strconv.FormatInt(result.Next, 10))
	}
	summary := map[string]any{"format": "household-audit-export-1", "entries": count,
		"final_chain": chain, "truncated": truncated, "exported_at": time.Now().UTC(), "release": buildinfo.Label()}
	if truncated {
		// Request the same filters with before=next_before for the older part.
		summary["next_before"] = last
	}
	manifest, _ := json.Marshal(map[string]any{"manifest": summary})
	lines.Write(manifest)
	lines.WriteByte('\n')
	// Recorded after the chain is final, so the export never contains its own
	// download event and a later export shows it as the newest entry.
	if err := s.recordExport(r, "audit_log", "audit_exported"); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="household-audit-`+time.Now().Format("2006-01-02")+`.jsonl"`)
	s.writeBytes(w, r, lines.Bytes())
}

// clientErrorClasses are the error classes a report may name; the same list
// is in app.js. Messages are never accepted, because they can quote user input.
var clientErrorClasses = map[string]bool{"Error": true, "TypeError": true, "ReferenceError": true, "RangeError": true, "SyntaxError": true,
	"URIError": true, "EvalError": true, "AggregateError": true, "DOMException": true, "AbortError": true, "NetworkError": true,
	"QuotaExceededError": true, "SecurityError": true, "NotAllowedError": true, "InvalidStateError": true}

// clientError records a browser script error in the log, without personal
// data: an allowlisted error class, the script's file name, line and column.
func (s *Server) clientError(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if err := s.Auth.Limit(r.Context(), "client-error", id.UserID, 30, 15*time.Minute); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var report struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Kind   string `json:"kind"`
	}
	if err := readJSON(w, r, &report); err != nil {
		s.fail(w, r, err)
		return
	}
	clip := func(value string, limit int) string {
		value = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, value)
		if len(value) > limit {
			value = strings.ToValidUTF8(value[:limit], "")
		}
		return value
	}
	source := report.Source
	if parsed, err := url.Parse(source); err == nil {
		source = parsed.Path
	}
	if slash := strings.LastIndex(source, "/"); slash >= 0 {
		source = source[slash+1:]
	}
	kind := "error"
	if report.Kind == "rejection" {
		kind = "rejection"
	}
	// Only an allowlisted error class is logged, never free text.
	name := "other"
	if clientErrorClasses[report.Name] {
		name = report.Name
	}
	s.Log.Warn("browser script error", "request_id", trace(r), "kind", kind, "class", name,
		"source", clip(source, 80), "line", max(report.Line, 0), "column", max(report.Column, 0))
	w.WriteHeader(http.StatusNoContent)
}

// helpPage explains Household's own terms.
func (s *Server) helpPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Hilfe")
	p.View = "help"
	s.render(w, r, p)
}
