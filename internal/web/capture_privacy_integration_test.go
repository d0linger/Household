//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestCapturePrivacySourceDeletion(t *testing.T) {
	f, source, _, capture, sentinel := capturePrivacyFixture(t)
	assertCapturePrivacy(t, f, capture, sentinel, http.StatusOK)
	// The released read-only capture must never reparent a now immutable original.
	confirm := fmt.Sprintf(`{"generation":%d,"version":0,"data":{"title":"Must not duplicate"}}`, f.app.Store.States()["documents"].Generation)
	denied, _, err := f.request(http.MethodPost, "/capture/"+capture+"/confirm", f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", strings.NewReader(confirm), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || denied.StatusCode != http.StatusUnprocessableEntity {
		t.Fatal("legacy confirmation must use current workspace", denied, err)
	}
	current, err := f.app.Store.DocumentDetail(f.ctx, f.identity, source)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"generation":%d,"version":%d,"action":"archive","id":%q,"data":{"archived":true}}`, f.app.Store.States()["documents"].Generation, store.Int(current["version"]), source)
	response, data, err := f.request(http.MethodPost, "/api/v1/documents/commands", f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", strings.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("source archive status = %d: %s", response.StatusCode, data)
	}
	var intact bool
	if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM documents.documents d JOIN documents.file_versions v ON v.document_id=d.id JOIN core.attachments a ON a.id=v.attachment_id AND a.resource_id=d.id WHERE d.id=$1 AND d.archived_at IS NOT NULL) AND (SELECT count(*)=1 FROM documents.documents)`, source).Scan(&intact); err != nil || !intact {
		t.Fatal("archival or legacy denial changed originals", intact, err)
	}
	assertCapturePrivacy(t, f, capture, sentinel, http.StatusNotFound)
}

func TestCapturePrivacyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, update, target string
	}{
		{"orphaned_attachment", "UPDATE core.attachments SET state='orphaned' WHERE id=$1", "attachment"},
		{"old_attachment_generation", "UPDATE core.attachments SET module_generation=module_generation+1 WHERE id=$1", "attachment"},
		{"different_attachment_module", "UPDATE core.attachments SET module_id='shopping' WHERE id=$1", "attachment"},
		{"missing_source", "UPDATE core.attachments SET resource_id=gen_random_uuid() WHERE resource_id=$1", "source"},
		{"old_capture_generation", "UPDATE core.capture_jobs SET module_generation=module_generation+1 WHERE id=$1", "capture"},
		{"expired_capture", "UPDATE core.capture_jobs SET expires_at=now()-interval '1 second' WHERE id=$1", "capture"},
		{"disabled_module", "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='documents'", "household"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, source, attachment, capture, sentinel := capturePrivacyFixture(t)
			assertCapturePrivacy(t, f, capture, sentinel, http.StatusOK)
			if tc.name == "missing_source" {
				if _, err := f.owner.Exec(f.ctx, "DELETE FROM documents.documents WHERE id=$1", source); err == nil {
					t.Fatal("immutable version parent deletion was not fenced")
				}
			}
			targets := map[string]string{"attachment": attachment, "source": source, "capture": capture, "household": f.identity.HouseholdID}
			if _, err := f.owner.Exec(f.ctx, tc.update, targets[tc.target]); err != nil {
				t.Fatal(err)
			}
			assertCapturePrivacy(t, f, capture, sentinel, http.StatusNotFound)
		})
	}
}

func capturePrivacyFixture(t *testing.T) (*integrationFixture, string, string, string, string) {
	t.Helper()
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["documents"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	document, _ := auditCreate(t, f, "documents", "documents", store.Record{"title": "Capture privacy source"})
	source := store.String(document["id"])
	sentinel := "private-capture-" + value.UUID()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, text := range map[string]string{"csrf": f.app.Auth.CSRF(f.token, "session"), "generation": strconv.FormatInt(state.Generation, 10), "capture": "false", "document_version": "1", "current": "true"} {
		if err := form.WriteField(key, text); err != nil {
			t.Fatal(err)
		}
	}
	part, err := form.CreateFormFile("file", "capture-privacy.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(financeReceiptTestImage(t)); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	response, uploaded, err := f.request(http.MethodPost, "/api/v1/documents/"+source+"/original", f.token, f.app.Auth.CSRF(f.token, "session"), form.FormDataContentType(), &body, map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatal("actual nonfinancial capture upload", response, err)
	}
	var receipt store.Record
	if err = json.Unmarshal(uploaded, &receipt); err != nil {
		t.Fatal(err)
	}
	capture, attachment := value.UUID(), store.String(receipt["attachment_id"])
	// This is synthetic pending text, not an OCR accuracy or worker claim.
	// Its actual App artifact reservation preserves the classified source.
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "documents", state.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	operation, err := f.app.Store.PrepareFinanceArtifact(f.ctx, tx, f.identity, "capture", capture, []store.FinanceResourceRef{{Kind: "attachment", ID: attachment}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.capture_jobs(id,household_id,module_id,module_generation,source_attachment_id,target_schema,state,extracted_values) VALUES($1,$2,'documents',$3,$4,'documents','review','{}')`, capture, f.identity.HouseholdID, state.Generation, attachment); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE core.capture_jobs SET source_text=$3,extracted_values=jsonb_build_object('title',$3::text) WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, capture, sentinel); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.CompleteFinanceArtifact(f.ctx, tx, f.identity, operation); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return f, source, attachment, capture, sentinel
}

func assertCapturePrivacy(t *testing.T, f *integrationFixture, capture, sentinel string, status int) {
	t.Helper()
	response, data, err := f.request(http.MethodGet, "/capture/"+capture, f.token, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Errorf("capture status = %d, want %d", response.StatusCode, status)
	}
	if got := bytes.Contains(data, []byte(sentinel)); got != (status == http.StatusOK) {
		t.Errorf("private capture content visible = %t, want %t", got, status == http.StatusOK)
	}
}
