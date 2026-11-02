//go:build integration

package web

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"
)

func TestDocumentsHTTPOriginalIdentityReplayArchiveAndPrivateHeaders(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["documents"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	csrf := f.app.Auth.CSRF(f.token, "session")
	raw, _ := json.Marshal(store.DocumentCommand{Generation: 1, Action: "save", ID: "", Version: 0, Data: json.RawMessage(`{"title":"Exact uploaded document","category":"","issuer":"","notes":"","document_type":"other","fields":{},"reviewed":false}`)})
	response, body, err := f.request(http.MethodPost, "/api/v1/documents/commands", f.token, csrf, "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("create", response, string(body), err)
	}
	var d store.Record
	if json.Unmarshal(body, &d) != nil {
		t.Fatal(string(body))
	}
	rid := store.String(d["id"])
	var original bytes.Buffer
	if err = png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 11, 7))); err != nil {
		t.Fatal(err)
	}
	// A legal trailing PNG payload is retained exactly in the original; derived previews are separate.
	original.WriteString("household-original-provenance")
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	originalName := "abc" + strings.Repeat("ö", 200) + ".png"
	part, err := form.CreateFormFile("file", originalName)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(original.Bytes())
	_ = form.WriteField("generation", "1")
	_ = form.WriteField("document_version", "1")
	_ = form.WriteField("current", "true")
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	key := value.UUID()
	response, body, err = f.request(http.MethodPost, "/api/v1/documents/"+rid+"/original", f.token, csrf, form.FormDataContentType(), bytes.NewReader(upload.Bytes()), map[string]string{"Idempotency-Key": key})
	if err != nil || response.StatusCode != 201 {
		t.Fatal("upload", response, string(body), err)
	}
	var receipt store.Record
	if json.Unmarshal(body, &receipt) != nil {
		t.Fatal(string(body))
	}
	var storedName string
	if err = f.owner.QueryRow(f.ctx, `SELECT original_name FROM core.attachments WHERE id=$1`, receipt["attachment_id"]).Scan(&storedName); err != nil || storedName != documentOriginalName(originalName) {
		t.Fatal("Unicode original name", storedName, err)
	}
	response, repeated, err := f.request(http.MethodPost, "/api/v1/documents/"+rid+"/original", f.token, csrf, form.FormDataContentType(), bytes.NewReader(upload.Bytes()), map[string]string{"Idempotency-Key": key})
	if err != nil || response.StatusCode != 200 || !bytes.Equal(body, repeated) {
		t.Fatal("durable upload replay", response, string(repeated), err)
	}
	response, download, err := f.request(http.MethodGet, "/attachments/"+store.String(receipt["attachment_id"]), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !bytes.Equal(download, original.Bytes()) {
		t.Fatal("original changed", response, err)
	}
	response, archive, err := f.request(http.MethodGet, "/api/v1/documents/export.zip?ids="+rid, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatal("export", response, string(archive), err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	seenOriginal, seenManifest := false, false
	digest := sha256.Sum256(original.Bytes())
	for _, file := range reader.File {
		in, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(in)
		_ = in.Close()
		if e != nil {
			t.Fatal(e)
		}
		if file.Name == "manifest.json" {
			seenManifest = bytes.Contains(data, []byte(hex.EncodeToString(digest[:])))
			if bytes.Contains(data, []byte("storage_key")) {
				t.Fatal("internal path exported")
			}
		} else {
			seenOriginal = bytes.Equal(data, original.Bytes())
		}
	}
	if !seenManifest || !seenOriginal {
		t.Fatal("portable archive missing exact original or hash", seenManifest, seenOriginal)
	}
	response, _, err = f.request(http.MethodGet, "/api/v1/documents/detail?id="+rid, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("private detail headers", response, err)
	}
	readerMember := shoppingTestMember(t, f, "documents-reader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, readerMember.Username, integrationPassword, "", "127.0.0.1", "documents-reader")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/documents/context", "/api/v1/documents/detail?id=" + rid, "/api/v1/documents/library?search=Exact", "/attachments/" + store.String(receipt["attachment_id"]), "/api/v1/documents/export.zip?ids=" + rid} {
		read, content, readErr := f.request(http.MethodGet, path, readerToken, "", "", nil, nil)
		if readErr != nil || read.StatusCode != 200 {
			t.Fatal("actual reader surface", path, read, readErr)
		}
		if path == "/api/v1/documents/context" && (!bytes.Contains(content, []byte(`"can_write":false`)) || !bytes.Contains(content, []byte(`"can_configure":false`))) {
			t.Fatal("reader granted configuration or write capability")
		}
	}
	denied, _, err := f.request(http.MethodPost, "/api/v1/documents/commands", readerToken, f.app.Auth.CSRF(readerToken, "session"), "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || denied.StatusCode != http.StatusForbidden {
		t.Fatal("actual reader wrote metadata", denied, err)
	}
	denied, _, err = f.request(http.MethodGet, "/api/v1/documents/integrations", readerToken, "", "", nil, nil)
	if err != nil || denied.StatusCode != http.StatusForbidden {
		t.Fatal("reader inspected operator inputs", denied, err)
	}
}

func TestDocumentsHTTPActualWatchImportPreservesSourceAndDeduplicatesAcrossApproval(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["documents"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	generation := f.app.Store.States()["documents"].Generation
	inbox := t.TempDir()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(inbox, "scan.ready.png")
	if err := os.WriteFile(source, imageBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(source, past, past); err != nil {
		t.Fatal(err)
	}
	approve := func(version int64) {
		t.Helper()
		raw := json.RawMessage(`{"adapter":"watch","enabled":true}`)
		if _, err := f.app.Store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.DocumentCommand{Generation: generation, Action: "integration", Version: version, Data: raw}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := f.app.Cfg
	cfg.DocumentInputs.WatchDir = inbox
	cfg.OCREnabled = false
	w := &worker.Worker{Pool: f.pool, Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer func() { _ = w.Close() }()
	approve(0)
	if err := w.Tick(f.ctx); err != nil {
		t.Fatal("actual watch worker", err)
	}
	var documents, receipts int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM documents.documents),(SELECT count(*) FROM documents.import_receipts)`).Scan(&documents, &receipts); err != nil || documents != 1 || receipts != 1 {
		t.Fatal("first import", documents, receipts, err)
	}
	var rid, attachment string
	if err := f.owner.QueryRow(f.ctx, `SELECT v.document_id,a.storage_key FROM documents.file_versions v JOIN core.attachments a ON a.id=v.attachment_id WHERE v.source='watch'`).Scan(&rid, &attachment); err != nil {
		t.Fatal(err)
	}
	write := func(action, id string, version int64, data store.Record) store.Record {
		t.Helper()
		body, e := json.Marshal(data)
		if e != nil {
			t.Fatal(e)
		}
		out, e := f.app.Store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.DocumentCommand{Generation: generation, Action: action, ID: id, Version: version, Data: body})
		if e != nil {
			t.Fatal(action, e)
		}
		return out
	}
	rule := write("rule_save", "", 0, store.Record{"name": "Actual imported scan classification", "contains_text": "scan.ready", "document_type": "invoice", "issuer": "Suggested issuer", "category": "Suggested import", "enabled": true})
	current, err := f.app.Store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	proposal := store.Record{"rule_id": rule["id"], "rule_version": 1, "items": []store.Record{{"id": rid, "version": current["version"]}}}
	write("rule_preview", "", 0, proposal)
	write("rule_apply", "", 0, proposal)
	current, err = f.app.Store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil || current["issuer"] != "Suggested issuer" || current["document_type"] != "invoice" {
		t.Fatal("actual imported classification", current, err)
	}
	write("save", rid, store.Int(current["version"]), store.Record{"title": "Local scanned record", "document_type": "invoice", "issuer": "Manual issuer", "category": "Manual category", "notes": "Keep local changes on provider replay", "fields": store.Record{"reference": "LOCAL001"}, "reviewed": false})
	// Reset only the disposable source cursor to replay the real provider input after re-approval.
	if _, err := f.owner.Exec(f.ctx, `UPDATE documents.integration_sources SET cursor='{}',next_poll_at=clock_timestamp() WHERE adapter='watch'`); err != nil {
		t.Fatal(err)
	}
	approve(1)
	if err := w.Tick(f.ctx); err != nil {
		t.Fatal("actual provider replay", err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM documents.documents),(SELECT count(*) FROM documents.import_receipts)`).Scan(&documents, &receipts); err != nil || documents != 1 || receipts != 1 {
		t.Fatal("provider replay duplicated original", documents, receipts, err)
	}
	root, err := os.OpenRoot(f.app.Cfg.UploadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	stored, err := root.ReadFile(attachment)
	if err != nil || !bytes.Equal(stored, imageBytes.Bytes()) {
		t.Fatal("local copy changed source bytes", err)
	}
	retained, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(retained, imageBytes.Bytes()) {
		t.Fatal("watch source moved or deleted", err)
	}
	detail, err := f.app.Store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil || detail["review_state"] != "unreviewed" || detail["title"] != "Local scanned record" || detail["issuer"] != "Manual issuer" || detail["category"] != "Manual category" {
		t.Fatal("import was silently reviewed", detail, err)
	}
}

func TestDocumentsHTTPActualWorkerFencesCorruptOriginalAndRecordsStatus(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["documents"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	generation := f.app.Store.States()["documents"].Generation
	raw := json.RawMessage(`{"title":"Actual document worker","category":"","issuer":"","notes":"","document_type":"other","fields":{},"reviewed":false}`)
	d, err := f.app.Store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.DocumentCommand{Generation: generation, Action: "save", Data: raw})
	if err != nil {
		t.Fatal(err)
	}
	rid := store.String(d["id"])
	root, err := os.OpenRoot(f.app.Cfg.UploadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	key, aid := value.UUID(), value.UUID()
	var imageBytes bytes.Buffer
	if err = png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile(key, imageBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(imageBytes.Bytes())
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.app.Store.BeginMutation(ctx, f.identity, "documents", generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,'documents',$3,'documents',$4,$5,'Worker.png','image/png',$6,$7,$8)`, aid, f.identity.HouseholdID, generation, rid, key, imageBytes.Len(), hash[:], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	original, err := store.RegisterDocumentOriginal(ctx, tx, f.identity, rid, aid, "upload", generation, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	queue := func() string {
		t.Helper()
		current, e := f.app.Store.DocumentDetail(f.ctx, f.identity, rid)
		if e != nil {
			t.Fatal(e)
		}
		data, _ := json.Marshal(store.Record{"version_id": original["version_id"]})
		out, e := f.app.Store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.DocumentCommand{Generation: generation, Action: "extract", ID: rid, Version: store.Int(current["version"]), Data: data})
		if e != nil {
			t.Fatal(e)
		}
		return store.String(out["processing_id"])
	}
	cfg := f.app.Cfg
	cfg.OCREnabled = false
	w := &worker.Worker{Pool: f.pool, Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer func() { _ = w.Close() }()
	pid := queue()
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	var jobState, processingState, code string
	var progress int
	if err = f.owner.QueryRow(f.ctx, `SELECT j.state,p.state,p.progress,p.status_code FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id WHERE p.id=$1`, pid).Scan(&jobState, &processingState, &progress, &code); err != nil || jobState != "done" || processingState != "done" || progress != 100 {
		t.Fatal("actual worker completion", jobState, processingState, progress, code, err)
	}
	if err = root.WriteFile(key, []byte("corrupt original"), 0600); err != nil {
		t.Fatal(err)
	}
	pid = queue()
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT j.state,p.state,p.progress,p.status_code FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id WHERE p.id=$1`, pid).Scan(&jobState, &processingState, &progress, &code); err != nil || jobState != "failed" || processingState != "failed" || code != "local_processing_failed" {
		t.Fatal("corrupt original was processed", jobState, processingState, progress, code, err)
	}
}
