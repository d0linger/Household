//go:build integration

package web

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"household/internal/pwned"
	"household/internal/value"
)

func TestHouseholdExportRequiresRecentAdminAndCarriesManifest(t *testing.T) {
	f := newIntegrationFixture(t)
	_, reader := policyAccount(t, f, "export-reader", "reader", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
	if response, _, err := f.request("GET", "/api/v1/core/export.zip", reader, "", "", nil, nil); err != nil || response.StatusCode != 403 {
		t.Fatalf("reader export: %v %v", response, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET reauthenticated_at=now()-interval '1 hour' WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	if response, body, err := f.request("GET", "/api/v1/core/export.zip", f.token, "", "", nil, nil); err != nil || response.StatusCode != 422 || !strings.Contains(string(body), "reauth_required") {
		t.Fatalf("stale admin export: %v %s %v", response, body, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET reauthenticated_at=now() WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	// Records the list views hide still belong in a complete export.
	var archivedList string
	if err := f.owner.QueryRow(f.ctx, `INSERT INTO shopping.lists(household_id,name,created_by,archived_at) VALUES($1,'Archivierte Liste',$2,now()) RETURNING id`,
		f.identity.HouseholdID, f.identity.UserID).Scan(&archivedList); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO shopping.lists(household_id,name,created_by,is_template) VALUES($1,'Vorlage Wocheneinkauf',$2,true)`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO shopping.items(household_id,list_id,name,created_by) VALUES($1,$2,'Artikel der archivierten Liste',$3)`, f.identity.HouseholdID, archivedList, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	response, body, err := f.request("GET", "/api/v1/core/export.zip", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/zip" ||
		!strings.HasPrefix(response.Header.Get("Content-Disposition"), `attachment; filename="household-export-`) {
		t.Fatalf("admin export: %v %v", response, err)
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*zip.File{}
	for _, file := range archive.File {
		files[file.Name] = file
	}
	manifestFile := files["manifest.json"]
	if manifestFile == nil {
		t.Fatalf("export lacks manifest: %v", files)
	}
	reader2, err := manifestFile.Open()
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Format  string `json:"format"`
		Entries []struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		} `json:"entries"`
	}
	if err = json.NewDecoder(reader2).Decode(&manifest); err != nil || manifest.Format != "household-export-1" || len(manifest.Entries) == 0 {
		t.Fatalf("manifest: %+v %v", manifest, err)
	}
	_ = reader2.Close()
	for _, entry := range manifest.Entries {
		if files[entry.File] == nil || len(entry.SHA256) != 64 || !strings.HasSuffix(entry.File, ".csv") {
			t.Fatalf("manifest entry without CSV file: %+v", entry)
		}
	}
	if len(files) != len(manifest.Entries)+1 {
		t.Fatalf("archive holds %d files for %d entries", len(files), len(manifest.Entries))
	}
	read := func(name string) string {
		t.Helper()
		file := files[name]
		if file == nil {
			t.Fatalf("export lacks %s", name)
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = handle.Close() }()
		data, err := io.ReadAll(handle)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if lists := read("shopping/lists.csv"); !strings.Contains(lists, "Archivierte Liste") || !strings.Contains(lists, "Vorlage Wocheneinkauf") {
		t.Fatal("export omits archived or template shopping lists")
	}
	if !strings.Contains(read("shopping/items.csv"), "Artikel der archivierten Liste") {
		t.Fatal("export omits items of archived shopping lists")
	}
	var events int
	var actor string
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*),coalesce(max(actor_user_id::text),'') FROM core.audit_log WHERE action='household_exported' AND resource_type='households'`).Scan(&events, &actor); err != nil ||
		events != 1 || actor != f.identity.UserID {
		t.Fatalf("household export audit: %d events by %q: %v", events, actor, err)
	}
}

func TestAuditExportChainsEveryVisibleEntry(t *testing.T) {
	f := newIntegrationFixture(t)
	values := url.Values{"username": {"audit-export-user"}, "display_name": {"Audit"}, "role": {"reader"}, "password": {"an audited start password 5521"}}
	if response, body, err := f.request("POST", "/settings/users", f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil); err != nil || response.StatusCode >= 400 {
		t.Fatalf("audited user creation: %v %s %v", response, body, err)
	}
	response, body, err := f.request("GET", "/api/v1/core/audit/export.jsonl", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("audit export: %v %s %v", response, body, err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	chain := auditChainStep("", []byte(auditChainSeed))
	count := 0
	var manifest struct {
		Manifest *struct {
			Format     string `json:"format"`
			Entries    int    `json:"entries"`
			FinalChain string `json:"final_chain"`
			Truncated  bool   `json:"truncated"`
		} `json:"manifest"`
		Entry json.RawMessage `json:"entry"`
		Chain string          `json:"chain"`
	}
	for scanner.Scan() {
		manifest.Manifest, manifest.Entry, manifest.Chain = nil, nil, ""
		if err = json.Unmarshal(scanner.Bytes(), &manifest); err != nil {
			t.Fatalf("line %d: %v", count+1, err)
		}
		if manifest.Manifest != nil {
			break
		}
		chain = auditChainStep(chain, manifest.Entry)
		if manifest.Chain != chain {
			t.Fatalf("chain breaks at line %d", count+1)
		}
		count++
	}
	if manifest.Manifest == nil || manifest.Manifest.Format != "household-audit-export-1" || manifest.Manifest.Entries != count ||
		manifest.Manifest.FinalChain != chain || manifest.Manifest.Truncated || count == 0 {
		t.Fatalf("manifest %+v after %d entries", manifest.Manifest, count)
	}
	if scanner.Scan() {
		t.Fatal("audit export continues after its manifest")
	}
	if strings.Contains(string(body), "audit_exported") {
		t.Fatal("audit export contains its own download event")
	}
	var events int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE action='audit_exported' AND resource_type='audit_log' AND actor_user_id=$1`, f.identity.UserID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("audit export audit: %d events: %v", events, err)
	}
	_, editor := policyAccount(t, f, "audit-export-editor", "editor", "Mozilla/5.0 (Windows NT 10.0) Edg/140.0")
	if response, _, err = f.request("GET", "/api/v1/core/audit/export.jsonl", editor, "", "", nil, nil); err != nil || response.StatusCode != 403 {
		t.Fatalf("editor audit export: %v %v", response, err)
	}
}

func TestConvenienceRoutesAnswerWithoutSideEffects(t *testing.T) {
	f := newIntegrationFixture(t)
	csrf := f.app.Auth.CSRF(f.token, "session")
	report := `{"name":"TypeError","source":"https://example.invalid/assets/app.js?v=1","line":12,"column":-4,"kind":"rejection"}`
	if response, body, err := f.request("POST", "/api/v1/core/client-errors", f.token, csrf, "application/json", strings.NewReader(report), nil); err != nil || response.StatusCode != 204 {
		t.Fatalf("client error report: %v %s %v", response, body, err)
	}
	// Free-text messages can quote user input and are refused outright.
	withMessage := `{"name":"TypeError","message":"Milch für Anna","source":"app.js","line":1,"column":1,"kind":"error"}`
	if response, _, err := f.request("POST", "/api/v1/core/client-errors", f.token, csrf, "application/json", strings.NewReader(withMessage), nil); err != nil || response.StatusCode != 422 {
		t.Fatalf("client error report with message: %v %v", response, err)
	}
	if response, _, err := f.request("POST", "/api/v1/core/client-errors", f.token, "", "application/json", strings.NewReader(report), nil); err != nil || response.StatusCode != 403 {
		t.Fatalf("client error report without CSRF: %v %v", response, err)
	}
	if response, _, err := f.request("POST", "/api/v1/core/client-errors", "", csrf, "application/json", strings.NewReader(report), nil); err != nil || response.StatusCode == 204 {
		t.Fatalf("anonymous client error report: %v %v", response, err)
	}
	html := map[string]string{"Accept": "text/html"}
	for path, want := range map[string]string{"/help": `id="probewiederherstellung"`, "/share": "data-share-list"} {
		response, body, err := f.request("GET", path, f.token, "", "", nil, html)
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), want) {
			t.Fatalf("%s: %v %v", path, response, err)
		}
	}
	shared := map[string]string{"Origin": "null", "Sec-Fetch-Site": "none"}
	response, _, err := f.request("POST", "/share", "", "", "multipart/form-data; boundary=x", strings.NewReader("--x--\r\n"), shared)
	if err != nil || response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/share?missed=1" {
		t.Fatalf("share fallback: %v %v", response, err)
	}
}

func TestBreachedPasswordsAreRefusedWhenCheckIsOn(t *testing.T) {
	f := newIntegrationFixture(t)
	const breached = "correct horse battery staple 42"
	var calls atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if len(strings.TrimPrefix(r.URL.Path, "/range/")) != 5 || r.Header.Get("Add-Padding") != "true" {
			http.Error(w, "bad request", 400)
			return
		}
		// SHA-1 of the breached password, upper case, without the prefix.
		_, _ = fmt.Fprintf(w, "0000000000000000000000000000000000A:0\r\n%s:37\r\n", breachedSuffix(breached))
	}))
	defer service.Close()
	previous := pwned.Endpoint
	pwned.Endpoint = service.URL + "/range/"
	defer func() { pwned.Endpoint = previous }()
	f.app.checks.breach = pwned.New()
	csrf := f.app.Auth.CSRF(f.token, "session")
	values := url.Values{"username": {"breach-check-user"}, "display_name": {"Breach"}, "role": {"reader"}, "password": {breached}}
	response, body, err := f.request("POST", "/settings/users", f.token, csrf, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil)
	if err != nil || response.StatusCode != 422 || !strings.Contains(string(body), "Datenlecks") {
		t.Fatalf("breached start password: %v %s %v", response, body, err)
	}
	values.Set("password", "an unlisted start password 9137")
	values.Set("username", "breach-check-ok")
	if response, body, err = f.request("POST", "/settings/users", f.token, csrf, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil); err != nil || response.StatusCode >= 400 {
		t.Fatalf("clean start password: %v %s %v", response, body, err)
	}
	service.Close()
	values.Set("password", breached)
	values.Set("username", "breach-check-offline")
	if response, body, err = f.request("POST", "/settings/users", f.token, csrf, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil); err != nil || response.StatusCode >= 400 {
		t.Fatalf("unreachable check must not block: %v %s %v", response, body, err)
	}
	var created int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.users WHERE username IN ('breach-check-user','breach-check-ok','breach-check-offline')").Scan(&created); err != nil || created != 2 || calls.Load() != 2 {
		t.Fatalf("created %d users after %d lookups: %v", created, calls.Load(), err)
	}
}

func breachedSuffix(password string) string {
	sum := sha1.Sum([]byte(password)) // #nosec G401 -- mirrors the HIBP range protocol
	return strings.ToUpper(hex.EncodeToString(sum[:]))[5:]
}

// TestSharedFileSavesThroughTheShareScriptRequests replays exactly the
// requests share.js sends when a member saves a shared photo as a document.
func TestSharedFileSavesThroughTheShareScriptRequests(t *testing.T) {
	f := newIntegrationFixture(t)
	if state := f.app.Store.States()["documents"]; state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	csrf := f.app.Auth.CSRF(f.token, "session")
	response, body, err := f.request("GET", "/api/v1/core/bootstrap", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("bootstrap: %v %v", response, err)
	}
	var bootstrap struct {
		States map[string]struct {
			State      string `json:"state"`
			Generation int64  `json:"generation"`
		} `json:"states"`
	}
	if err = json.Unmarshal(body, &bootstrap); err != nil || bootstrap.States["documents"].State != "enabled" {
		t.Fatalf("bootstrap states: %s %v", body, err)
	}
	generation := bootstrap.States["documents"].Generation
	create, _ := json.Marshal(map[string]any{"data": map[string]any{"title": "Geteilter Beleg"}, "generation": generation})
	response, body, err = f.request("POST", "/api/v1/m/documents/documents", f.token, csrf, "application/json", bytes.NewReader(create), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("create document like share.js: %v %s %v", response, body, err)
	}
	var record struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	if err = json.Unmarshal(body, &record); err != nil || record.ID == "" || record.Version < 1 {
		t.Fatalf("created record: %s %v", body, err)
	}
	var photo bytes.Buffer
	if err = png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 9, 5))); err != nil {
		t.Fatal(err)
	}
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, err := form.CreateFormFile("file", "beleg.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(photo.Bytes())
	_ = form.WriteField("generation", strconv.FormatInt(generation, 10))
	_ = form.WriteField("document_version", strconv.FormatInt(record.Version, 10))
	_ = form.WriteField("current", "true")
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	response, body, err = f.request("POST", "/api/v1/documents/"+record.ID+"/original", f.token, csrf, form.FormDataContentType(), bytes.NewReader(upload.Bytes()), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode >= 300 {
		t.Fatalf("upload like share.js: %v %s %v", response, body, err)
	}
	response, _, err = f.request("GET", "/m/documents/workspace/review?id="+url.QueryEscape(record.ID), f.token, "", "", nil, map[string]string{"Accept": "text/html"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("document link from share.js: %v %v", response, err)
	}
	var originals int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.attachments WHERE resource_id::text=$1`, record.ID).Scan(&originals); err != nil || originals != 1 {
		t.Fatalf("stored originals %d: %v", originals, err)
	}
}
