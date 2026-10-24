//go:build integration

package web

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"household/internal/value"
)

func TestCapturePrivacySourceDeletion(t *testing.T) {
	f, source, _, capture, sentinel := capturePrivacyFixture(t)
	assertCapturePrivacy(t, f, capture, sentinel, http.StatusOK)
	body := fmt.Sprintf(`{"generation":%d,"version":1,"data":{}}`, f.app.Store.States()["documents"].Generation)
	response, data, err := f.request(http.MethodDelete, "/api/v1/m/documents/documents/"+source, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", strings.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("source deletion status = %d: %s", response.StatusCode, data)
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
		{"missing_source", "DELETE FROM documents.documents WHERE id=$1", "source"},
		{"old_capture_generation", "UPDATE core.capture_jobs SET module_generation=module_generation+1 WHERE id=$1", "capture"},
		{"expired_capture", "UPDATE core.capture_jobs SET expires_at=now()-interval '1 second' WHERE id=$1", "capture"},
		{"disabled_module", "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='documents'", "household"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, source, attachment, capture, sentinel := capturePrivacyFixture(t)
			assertCapturePrivacy(t, f, capture, sentinel, http.StatusOK)
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
	source, attachment, capture := value.UUID(), value.UUID(), value.UUID()
	sentinel := "private-capture-" + value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO documents.documents(id,household_id,title,created_by) VALUES($1,$2,'Capture privacy source',$3)", source, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,'documents',$3,'documents',$4,$5,'fixture.png','image/png',1,$6,$7)", attachment, f.identity.HouseholdID, state.Generation, source, value.UUID(), make([]byte, 32), f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.capture_jobs(id,household_id,module_id,module_generation,source_attachment_id,target_schema,source_text,extracted_values) VALUES($1,$2,'documents',$3,$4,'documents',$5,jsonb_build_object('title',$5::text))", capture, f.identity.HouseholdID, state.Generation, attachment, sentinel); err != nil {
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
