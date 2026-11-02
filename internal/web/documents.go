package web

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"household/internal/document"
	"household/internal/store"
	"household/internal/value"
)

var documentPanes = []string{"inbox", "library", "review", "deadlines", "rules", "processing", "integrations", "duplicates"}

func documentOriginalName(name string) string {
	runes := []rune(name)
	if len(runes) <= 200 {
		return string(runes)
	}
	extension := []rune(filepath.Ext(name))
	if len(extension) > 10 {
		extension = nil
	}
	return string(runes[:200-len(extension)]) + string(extension)
}

func (s *Server) registerDocumentRoutes(mux *http.ServeMux) {
	mux.Handle("GET /m/documents/workspace/{pane}", s.require(s.documentPage))
	mux.Handle("GET /api/v1/documents/{view}", s.require(s.documentAPI))
	mux.Handle("POST /api/v1/documents/commands", s.require(s.documentAPI))
	mux.Handle("POST /api/v1/documents/{id}/original", s.require(s.documentUpload))
	mux.Handle("GET /api/v1/documents/export.zip", s.require(s.documentExport))
}

func documentQuery(r *http.Request) (url.Values, error) {
	if len(r.URL.RawQuery) > 6000 {
		return nil, store.Invalid{Message: "Dokumentfilter zu groß"}
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, store.Invalid{Message: "Dokumentfilter prüfen"}
	}
	for key, values := range q {
		if !slices.Contains([]string{"id", "search", "document_type", "state", "issuer", "category", "due_before", "cursor", "ids", "module", "entity"}, key) || len(values) != 1 {
			return nil, store.Invalid{Message: "Unbekannter oder mehrfacher Dokumentfilter"}
		}
	}
	return q, nil
}

func (s *Server) documentPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	w.Header().Set("Cache-Control", "private, no-store")
	if !slices.Contains(documentPanes, r.PathValue("pane")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	q, err := documentQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if q.Get("id") != "" && !value.ValidUUID(q.Get("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	initial, err := s.Store.DocumentContext(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Dokumente & Fristen")
	p.Module, _ = s.Store.Module("documents")
	p.View = "documents-workspace"
	p.Extra = store.Record{"pane": r.PathValue("pane"), "document_id": q.Get("id"), "generation": initial["generation"]}
	s.render(w, r, p)
}

func readDocumentJSON(w http.ResponseWriter, r *http.Request, in *store.DocumentCommand) error {
	invalid := store.Invalid{Message: "Dokumentvorgang enthält ungültige, mehrfache oder unbekannte Felder"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return invalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	remaining := 10000
	if documentJSONValue(d, 0, &remaining) != nil {
		return invalid
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return invalid
	}
	for _, key := range []string{"generation", "version", "action", "id", "data"} {
		if _, ok := members[key]; !ok {
			return invalid
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(in) != nil || d.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

func documentJSONValue(d *json.Decoder, depth int, remaining *int) error {
	*remaining--
	if depth > 10 || *remaining < 0 {
		return errors.New("document JSON limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("document JSON structure")
	}
	seen := map[string]bool{}
	for d.More() {
		if delimiter == '{' {
			key, e := d.Token()
			name, ok := key.(string)
			if e != nil || !ok || seen[name] {
				return errors.New("duplicate document JSON member")
			}
			seen[name] = true
		}
		if err = documentJSONValue(d, depth+1, remaining); err != nil {
			return err
		}
	}
	closeToken, err := d.Token()
	if err != nil || delimiter == '{' && closeToken != json.Delim('}') || delimiter == '[' && closeToken != json.Delim(']') {
		return errors.New("document JSON incomplete")
	}
	return nil
}

func (s *Server) documentAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	var out store.Record
	var err error
	if r.Method == http.MethodPost {
		var in store.DocumentCommand
		err = readDocumentJSON(w, r, &in)
		if err == nil {
			out, err = s.Store.DocumentWrite(r.Context(), identity(r), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	} else {
		var q url.Values
		q, err = documentQuery(r)
		if err == nil {
			switch r.PathValue("view") {
			case "context":
				out, err = s.Store.DocumentContext(r.Context(), identity(r))
				if err == nil && identity(r).Role == "admin" {
					out["available_inputs"] = store.Record{"watch": s.Cfg.DocumentInputs.Available("watch"), "imap": s.Cfg.DocumentInputs.Available("imap"), "paperless": s.Cfg.DocumentInputs.Available("paperless")}
				}
			case "library":
				out, err = s.Store.DocumentLibrary(r.Context(), identity(r), document.Filters{Search: q.Get("search"), Type: q.Get("document_type"), State: q.Get("state"), Issuer: q.Get("issuer"), Category: q.Get("category"), DueBefore: q.Get("due_before"), Cursor: q.Get("cursor")})
			case "detail":
				out, err = s.Store.DocumentDetail(r.Context(), identity(r), q.Get("id"))
			case "targets":
				out, err = s.Store.DocumentTargets(r.Context(), identity(r), q.Get("module"), q.Get("entity"), q.Get("search"))
			default:
				out, err = s.Store.DocumentOverview(r.Context(), identity(r), r.PathValue("view"))
			}
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) documentUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	r.SetPathValue("module", "documents")
	r.SetPathValue("entity", "documents")
	s.upload(w, r)
}

func (s *Server) documentExport(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q, err := documentQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	archive, err := s.Store.DocumentExport(r.Context(), identity(r), strings.Split(q.Get("ids"), ","))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	root, err := os.OpenRoot(s.Cfg.UploadDir)
	if err != nil && len(archive.Files) > 0 {
		s.fail(w, r, err)
		return
	}
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	// Build a bounded archive before writing HTTP headers, so missing or corrupt files fail atomically.
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest, err := json.MarshalIndent(archive.Manifest, "", "  ")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	entry, err := writer.Create("manifest.json")
	if err == nil {
		_, err = entry.Write(manifest)
	}
	for _, file := range archive.Files {
		if err != nil {
			break
		}
		if r.Context().Err() != nil {
			err = r.Context().Err()
			break
		}
		var in *os.File
		in, err = root.Open(file.Key)
		if err != nil {
			break
		}
		var raw []byte
		raw, err = io.ReadAll(io.LimitReader(in, file.Size+1))
		_ = in.Close()
		if err != nil {
			break
		}
		hash := sha256.Sum256(raw)
		if int64(len(raw)) != file.Size || hex.EncodeToString(hash[:]) != file.SHA256 {
			err = store.Invalid{Message: "Originaldatei stimmt nicht mit ihrer gesicherten Prüfsumme überein"}
			break
		}
		entry, err = writer.CreateHeader(&zip.FileHeader{Name: file.Path, Method: zip.Store})
		if err == nil {
			_, err = entry.Write(raw)
		}
		if buffer.Len() > 70<<20 {
			err = store.Invalid{Message: "Archivgrenze erreicht"}
		}
	}
	closeErr := writer.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="household-documents.zip"`)
	s.writeBytes(w, r, buffer.Bytes())
}
