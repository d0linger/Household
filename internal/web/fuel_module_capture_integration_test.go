//go:build integration

package web

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
)

func fuelModuleCaptureUpload(t *testing.T, f *integrationFixture, vehicle string) string {
	t.Helper()
	var picture, body bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "synthetic-native-photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(picture.Bytes()); err != nil {
		t.Fatal(err)
	}
	for field, text := range map[string]string{"csrf": f.app.Auth.CSRF(f.token, "session"), "generation": strconv.FormatInt(f.app.Store.States()["fuel"].Generation, 10), "capture": "false"} {
		if err = writer.WriteField(field, text); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodPost, "/api/v1/m/fuel/vehicles/"+vehicle+"/attachments", f.token, "", writer.FormDataContentType(), &body, nil)
	if err != nil || response.StatusCode != http.StatusSeeOther {
		t.Fatal("actual private multipart upload", response, err)
	}
	var attachment string
	if err = f.pool.QueryRow(f.ctx, "SELECT id FROM core.attachments WHERE resource_id=$1 AND state='ready'", vehicle).Scan(&attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}

func fuelModuleCaptureStage(t *testing.T, f *integrationFixture, vehicle, series, attachment string) store.FuelModuleResult {
	t.Helper()
	result, _, _ := fuelModuleNativeApply(t, f, "capture.stage", vehicle, "", 0,
		store.Record{"kind": "odometer", "attachment_id": attachment, "odometer_series_id": series}, nil)
	return result
}

func TestFuelModuleCaptureLeaseProposalAndExplicitPhotoConfirmation(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	attachment := fuelModuleCaptureUpload(t, f, vid)
	f.app.Store.FuelOCREnabled = true
	job := fuelModuleCaptureStage(t, f, vid, sid, attachment)
	if job.Data["state"] != "queued" {
		t.Fatal("enabled local extractor must queue a proposal", job)
	}
	// A bare worker marker is not a capability to change a captured proposal.
	ctx := audit.WithHousehold(audit.WithSystem(f.ctx, "worker"), f.identity.HouseholdID)
	tx, err := audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('household.fuel_capture_job',$1,true)", job.RecordID); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, "UPDATE fuel.capture_jobs SET state='review',source_text='forged',version=version+1 WHERE id=$1", job.RecordID)
	fuelModuleWriteRollback(tx)
	fuelModuleWriteSQLCode(t, err, "42501")
	calls := 0
	err = f.app.Store.FuelModuleCaptureHousekeeping(f.ctx, true, func(ctx context.Context, source store.FuelCaptureSource) (string, error) {
		calls++
		if ctx.Err() != nil || source.JobID != job.RecordID || source.AttachmentID != attachment || !value.ValidUUID(source.StorageKey) || len(source.SHA256) != 64 || source.SizeBytes < 1 || source.MediaType != "image/png" {
			t.Fatal("extractor did not receive the current immutable Core source", source)
		}
		var state, lease string
		if err := f.pool.QueryRow(ctx, "SELECT state,lease_token::text FROM fuel.capture_jobs WHERE id=$1", source.JobID).Scan(&state, &lease); err != nil || state != "running" || !value.ValidUUID(lease) {
			t.Fatal("OCR callback ran before committed lease", state, err)
		}
		// This deterministic callback tests the DB proposal seam, not Tesseract.
		return "Kilometerstand: 1234.500001 km", nil
	})
	if err != nil || calls != 1 {
		t.Fatal("actual capture claim/publish", calls, err)
	}
	var state, text, proposal, jobVersion string
	var facts, privateCapabilities int
	err = f.owner.QueryRow(f.ctx, `SELECT state,source_text,suggestions->'values'->>'odometer',version::text,
 (SELECT count(*) FROM fuel.odometer_observations),(SELECT count(*) FROM fuel.capture_transitions) FROM fuel.capture_jobs WHERE id=$1`, job.RecordID).
		Scan(&state, &text, &proposal, &jobVersion, &facts, &privateCapabilities)
	if err != nil || state != "review" || proposal != "1234.500001" || text == "" || facts != 0 || privateCapabilities != 0 {
		t.Fatal("proposal leaked a capability or invented a business fact", state, proposal, facts, privateCapabilities, err)
	}
	data := store.Record{"job_id": job.RecordID, "job_version": jobVersion, "values": store.Record{"observed_on": "2026-02-01", "reading": "1234.500001", "notes": "Explicitly reviewed synthetic photo"}}
	accepted, command, key := fuelModuleNativeApply(t, f, "capture.confirm", vid, "", 0, data, nil)
	var source, capture, reading string
	err = f.pool.QueryRow(f.ctx, "SELECT source_kind,capture_job_id::text,reading::text FROM fuel.odometer_observations WHERE id=$1", accepted.RecordID).Scan(&source, &capture, &reading)
	if err != nil || source != "photo" || capture != job.RecordID || reading != "1234.500001" {
		t.Fatal("confirmed photo provenance", source, capture, reading, err)
	}
	if _, err = f.app.Store.FuelModuleExecute(f.ctx, f.identity, key, value.UUID(), command); err != nil {
		t.Fatal("photo receipt replay", err)
	}
	if _, err = f.app.Store.FuelModulePreview(f.ctx, f.identity, fuelModuleNativeCommand(t, f, "capture.confirm", vid, "", 0, data, nil)); err == nil {
		t.Fatal("processed photo accepted a new confirmation")
	}
}

func TestFuelModuleCaptureRechecksAuthorityAndBoundedFailure(t *testing.T) {
	for _, test := range []string{"revoked_session", "changed_attachment", "disabled_extractor", "oversized_output", "failed_extractor"} {
		t.Run(test, func(t *testing.T) {
			f := newIntegrationFixture(t)
			vehicle, series := fuelModuleNativeVehicle(t, f)
			vid := store.String(vehicle["id"])
			attachment := fuelModuleCaptureUpload(t, f, vid)
			f.app.Store.FuelOCREnabled = true
			job := fuelModuleCaptureStage(t, f, vid, store.String(series["id"]), attachment)
			called := false
			err := f.app.Store.FuelModuleCaptureHousekeeping(f.ctx, test != "disabled_extractor", func(context.Context, store.FuelCaptureSource) (string, error) {
				called = true
				switch test {
				case "revoked_session":
					_, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", f.identity.SessionID)
					if err != nil {
						t.Fatal(err)
					}
				case "changed_attachment":
					_, err := f.owner.Exec(f.ctx, "UPDATE core.attachments SET sha256=decode(repeat('01',32),'hex') WHERE id=$1", attachment)
					if err != nil {
						t.Fatal(err)
					}
				case "oversized_output":
					return strings.Repeat("1", 65537), nil
				case "failed_extractor":
					return "must not persist partial text", errors.New("synthetic extraction failure")
				}
				return "Kilometerstand: 999 km", nil
			})
			if test == "revoked_session" || test == "changed_attachment" {
				if err == nil {
					t.Fatal("authority/source changed during extraction but publication was accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if called == (test == "disabled_extractor") {
				t.Fatal("extractor enablement boundary", test, called)
			}
			var state, text, proposal string
			var facts, caps int
			err = f.owner.QueryRow(f.ctx, `SELECT state,source_text,suggestions::text,(SELECT count(*) FROM fuel.odometer_observations),
 (SELECT count(*) FROM fuel.capture_transitions) FROM fuel.capture_jobs WHERE id=$1`, job.RecordID).Scan(&state, &text, &proposal, &facts, &caps)
			if err != nil || state == "review" || text != "" || facts != 0 || caps != 0 {
				t.Fatal("failed or unauthorized OCR published content/facts", state, text, proposal, facts, caps, err)
			}
		})
	}
}
