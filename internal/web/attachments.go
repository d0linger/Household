package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func sanitizeUpload(data []byte) ([]byte, string, error) {
	kind := http.DetectContentType(data)
	if kind == "application/pdf" {
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return nil, "", errors.New("ungültiges PDF")
		}
		return data, kind, nil
	}
	if kind != "image/jpeg" && kind != "image/png" {
		return nil, "", errors.New("nur JPEG, PNG und PDF zulässig")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		return nil, "", errors.New("bild ungültig oder größer als 20 Megapixel")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.New("bild kann nicht gelesen werden")
	}
	var clean bytes.Buffer
	if kind == "image/jpeg" {
		err = jpeg.Encode(&clean, decoded, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&clean, decoded)
	}
	if err != nil {
		return nil, "", err
	}
	if clean.Len() > 10<<20 {
		return nil, "", errors.New("aufbereitetes Bild ist größer als 10 MiB")
	}
	return clean.Bytes(), kind, nil
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !identity(r).CanWrite() {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	rid := r.PathValue("id")
	if !value.ValidUUID(rid) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	generation, _ := strconv.ParseInt(r.FormValue("generation"), 10, 64)
	file, header, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "Datei fehlt"})
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size > 10<<20 {
		s.fail(w, r, store.Invalid{Message: "Datei maximal 10 MiB"})
		return
	}
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	case <-r.Context().Done():
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 10<<20 {
		s.fail(w, r, store.Invalid{Message: "Datei leer oder zu groß"})
		return
	}
	clean, kind, err := sanitizeUpload(raw)
	if err != nil {
		s.fail(w, r, store.Invalid{Message: err.Error()})
		return
	}
	tx, err := s.Store.BeginMutation(r.Context(), identity(r), m.ID, generation)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "upload-quota/"+identity(r).HouseholdID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.Store.LockResource(r.Context(), tx, identity(r), m.ID, e.ID, rid); err != nil {
		s.fail(w, r, err)
		return
	}
	var used int64
	if err = tx.QueryRow(r.Context(), "SELECT coalesce(sum(size_bytes),0) FROM core.attachments WHERE household_id=$1 AND state='ready'", identity(r).HouseholdID).Scan(&used); err != nil {
		s.fail(w, r, err)
		return
	}
	if used+int64(len(clean)) > s.Cfg.UploadQuota {
		s.fail(w, r, store.Invalid{Message: "Speicherlimit des Haushalts erreicht"})
		return
	}
	if err = os.MkdirAll(s.Cfg.UploadDir, 0750); err != nil {
		s.fail(w, r, err)
		return
	}
	root, err := os.OpenRoot(s.Cfg.UploadDir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = root.Close() }()
	aid := value.UUID()
	key := value.UUID()
	out, err := root.OpenFile(key, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(key)
		}
	}()
	_, err = out.Write(clean)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if len(name) > 200 {
		name = name[:200]
	}
	sum := sha256.Sum256(clean)
	var financeSources []store.FinanceResourceRef
	if m.ID == "expenses" && (e.ID == "entries" || e.ID == "entry_lines") {
		financeSources = []store.FinanceResourceRef{{Kind: e.ID, ID: rid}}
	}
	financeOperation, err := s.Store.PrepareFinanceArtifact(r.Context(), tx, identity(r), "attachment", aid, financeSources)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)", aid, identity(r).HouseholdID, m.ID, generation, e.ID, rid, key, name, kind, len(clean), sum[:], identity(r).UserID)
	if err == nil && kind != "application/pdf" {
		payload, _ := json.Marshal(store.Record{"attachment_id": aid, "storage_key": key})
		_, err = tx.Exec(r.Context(), "INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key) VALUES($1,$2,$3,'thumbnail',$4,$5)", identity(r).HouseholdID, m.ID, generation, payload, "thumbnail/"+aid)
	}
	captureID := ""
	if err == nil && r.FormValue("capture") == "true" {
		captureID = value.UUID()
		captureOperation, prepareErr := s.Store.PrepareFinanceArtifact(r.Context(), tx, identity(r), "capture", captureID,
			[]store.FinanceResourceRef{{Kind: "attachment", ID: aid}})
		err = prepareErr
		if err == nil {
			_, err = tx.Exec(r.Context(), "INSERT INTO core.capture_jobs(id,household_id,module_id,module_generation,source_attachment_id,target_schema,extracted_values) VALUES($1,$2,$3,$4,$5,$6,'{}')", captureID, identity(r).HouseholdID, m.ID, generation, aid, e.ID)
		}
		if err == nil && s.Cfg.OCREnabled && kind != "application/pdf" {
			_, err = tx.Exec(r.Context(), "UPDATE core.capture_jobs SET state='extracting',provider='tesseract' WHERE id=$1", captureID)
			if err == nil {
				payload, _ := json.Marshal(store.Record{"capture_id": captureID, "storage_key": key})
				_, err = tx.Exec(r.Context(), "INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key) VALUES($1,$2,$3,'capture_ocr',$4,$5)", identity(r).HouseholdID, m.ID, generation, payload, "capture/"+captureID)
			}
		}
		if err == nil {
			err = s.Store.CompleteFinanceArtifact(r.Context(), tx, identity(r), captureOperation)
		}
	}
	if err == nil {
		err = store.Audit(r.Context(), tx, identity(r), m.ID, e.ID, rid, "attachment_upload", trace(r))
	}
	if err == nil {
		err = s.Store.CompleteFinanceArtifact(r.Context(), tx, identity(r), financeOperation)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	committed = true
	if captureID != "" {
		http.Redirect(w, r, "/capture/"+captureID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/m/"+m.ID+"/"+e.ID+"/"+rid, http.StatusSeeOther)
}
func (s *Server) attachmentList(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err = s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	actor := identity(r)
	rows, err := s.Pool.Query(r.Context(), `SELECT id,original_name,media_type,size_bytes,EXISTS(SELECT 1 FROM core.attachment_derivatives d
 WHERE d.attachment_id=a.id AND variant='preview' AND core.finance_artifact_visible($1,$5,$6,$7,$8,'attachment_derivative',d.id,'view'))
 FROM core.attachments a WHERE household_id=$1 AND module_id=$2 AND resource_type=$3 AND resource_id=$4 AND state='ready'
 AND core.finance_artifact_visible($1,$5,$6,$7,$8,'attachment',a.id,'view') ORDER BY created_at DESC LIMIT 100`, actor.HouseholdID,
		m.ID, e.ID, r.PathValue("id"), actor.UserID, actor.MembershipID, actor.SessionID, actor.AuthVersion)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	items := []store.Record{}
	for rows.Next() {
		var id, name, kind string
		var size int64
		var preview bool
		if err = rows.Scan(&id, &name, &kind, &size, &preview); err != nil {
			s.fail(w, r, err)
			return
		}
		items = append(items, store.Record{"id": id, "name": name, "media_type": kind, "size": size, "preview": preview})
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	var mid, entity, rid, key, name, kind string
	tx, err := s.Pool.Begin(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	actor := identity(r)
	err = tx.QueryRow(r.Context(), `SELECT a.module_id,a.resource_type,a.resource_id,a.storage_key,a.original_name,a.media_type FROM core.attachments a
 JOIN core.module_states m ON m.household_id=a.household_id AND m.module_id=a.module_id AND m.state='enabled' AND m.data_generation=a.module_generation
 WHERE a.id=$1 AND a.household_id=$2 AND a.state='ready' AND core.finance_artifact_visible($2,$3,$4,$5,$6,'attachment',a.id,'view')`,
		r.PathValue("id"), actor.HouseholdID, actor.UserID, actor.MembershipID, actor.SessionID, actor.AuthVersion).Scan(&mid, &entity, &rid, &key, &name, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, ok := s.Store.Module(mid)
	if !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	e, ok := m.Entity(entity)
	if !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, err = s.Store.Get(r.Context(), identity(r), m, e, rid); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.Store.LockFinanceArtifact(r.Context(), tx, actor, "attachment", r.PathValue("id"), "view"); err != nil {
		s.fail(w, r, err)
		return
	}
	if !value.ValidUUID(key) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	inline := strings.HasSuffix(r.URL.Path, "/preview")
	if inline {
		var derivativeID string
		if err = tx.QueryRow(r.Context(), "SELECT id,storage_key,media_type FROM core.attachment_derivatives WHERE attachment_id=$1 AND variant='preview'", r.PathValue("id")).Scan(&derivativeID, &key, &kind); err != nil {
			s.fail(w, r, store.ErrNotFound)
			return
		}
		if err = s.Store.LockFinanceArtifact(r.Context(), tx, actor, "attachment_derivative", derivativeID, "view"); err != nil {
			s.fail(w, r, err)
			return
		}
		if !value.ValidUUID(key) {
			s.fail(w, r, store.ErrNotFound)
			return
		}
	}
	root, err := os.OpenRoot(s.Cfg.UploadDir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(key)
	if err != nil {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if inline {
		w.Header().Set("Content-Disposition", "inline")
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, name, info.ModTime(), file)
}
func (s *Server) capturePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	var mid, entity, aid, state, source, sourceType, sourceID string
	var generation int64
	var raw []byte
	err := s.Pool.QueryRow(r.Context(), `SELECT c.module_id,c.target_schema,c.source_attachment_id,c.state,c.module_generation,
		coalesce(c.extracted_values,'{}'),c.source_text,a.resource_type,a.resource_id
		FROM core.capture_jobs c JOIN core.attachments a ON a.id=c.source_attachment_id
		AND a.household_id=c.household_id AND a.module_id=c.module_id AND a.module_generation=c.module_generation AND a.state='ready'
		JOIN core.module_states m ON m.household_id=c.household_id AND m.module_id=c.module_id
		AND m.state='enabled' AND m.data_generation=c.module_generation
		WHERE c.id=$1 AND c.household_id=$2 AND c.expires_at>now()
		AND core.finance_artifact_visible($2,$3,$4,$5,$6,'capture',c.id,'view')`, r.PathValue("id"), identity(r).HouseholdID,
		identity(r).UserID, identity(r).MembershipID, identity(r).SessionID, identity(r).AuthVersion).Scan(&mid, &entity, &aid, &state, &generation, &raw, &source, &sourceType, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, known := s.Store.Module(mid)
	st, enabled := s.Store.State(mid)
	e, exists := m.Entity(entity)
	if !known || !enabled || !exists || st.Generation != generation {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	sourceEntity, sourceExists := m.Entity(sourceType)
	if !sourceExists {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	// OCR content follows the same live source boundary as private downloads.
	if _, err = s.Store.Get(r.Context(), identity(r), m, sourceEntity, sourceID); err != nil {
		s.fail(w, r, err)
		return
	}
	if state == "extracting" {
		p := s.page(r, "Lokale Texterkennung läuft")
		p.View = "message"
		p.Success = "Das Bild wird lokal verarbeitet. Öffne diese Erfassung in wenigen Sekunden erneut."
		p.Extra = map[string]any{"refresh": "/capture/" + r.PathValue("id")}
		s.render(w, r, p)
		return
	}
	if state != "review" {
		s.fail(w, r, store.ErrConflict)
		return
	}
	p := s.page(r, "Erfassung prüfen")
	p.Module = m
	p.Entity = e
	p.Record = store.Record{}
	if p.Record, err = store.DecodeRecord(raw); err != nil {
		s.fail(w, r, err)
		return
	}
	p.References, err = s.references(r, e, p.Record)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.View = "capture"
	p.Extra = map[string]any{"id": r.PathValue("id"), "attachment_id": aid, "source_text": source}
	s.render(w, r, p)
}
func (s *Server) confirmCapture(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	in, _, err := s.mutation(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rid, mid, entity, err := s.Store.ConfirmCapture(r.Context(), identity(r), r.PathValue("id"), trace(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, store.Record{"id": rid, "url": "/m/" + mid + "/" + entity + "/" + rid})
}
