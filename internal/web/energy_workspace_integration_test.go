//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func energyHTTPData(t *testing.T, data store.Record) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEnergyHTTPAuthorityAssetsStrictCSRFAndDurablePhoto(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["meters"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "meters", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["meters"]
	}
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, pane := range energyPanes {
		response, raw, err := f.request(http.MethodGet, "/m/meters/workspace/"+pane, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte("meters-workspace.js")) || !bytes.Contains(raw, []byte("meters.css")) {
			t.Fatal("workspace local assets", pane, err)
		}
	}
	command := store.EnergyCommand{Generation: state.Generation, Action: "meter.save", Data: energyHTTPData(t, store.Record{"name": "HTTP actual meter", "unit_code": "kWh"})}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodPost, "/api/v1/meters/commands", f.token, "", "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("missing CSRF accepted", err)
	}
	response, body, err := f.request(http.MethodPost, "/api/v1/meters/commands", f.token, csrf, "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("actual command", response.StatusCode, string(body), err)
	}
	var meter store.Record
	if err = json.Unmarshal(body, &meter); err != nil {
		t.Fatal(err)
	}
	var register string
	if err = f.owner.QueryRow(f.ctx, `SELECT id::text FROM meters.registers WHERE meter_id=$1 AND is_default`, meter["id"]).Scan(&register); err != nil {
		t.Fatal(err)
	}
	write := func(action string, data store.Record) store.Record {
		t.Helper()
		out, err := f.app.Store.EnergyWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.EnergyCommand{Generation: state.Generation, Action: action, Data: energyHTTPData(t, data)})
		if err != nil {
			t.Fatal(action, err)
		}
		return out
	}
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	device := write("device.save", store.Record{"register_id": register, "register_version": "1", "meter_id": meter["id"], "serial_number": "HTTP physical meter", "installed_at": at.Add(-time.Hour).Format(time.RFC3339), "initial_value": "0"})
	reading := write("reading.save", store.Record{"register_id": register, "register_version": "1", "device_id": device["id"], "measured_at": at.Format(time.RFC3339), "value": "123.000001", "kind": "actual"})
	reader := shoppingTestMember(t, f, "energy-http-reader", "reader")
	if _, err = f.app.Store.EnergyWrite(f.ctx, reader, value.UUID(), value.UUID(), command); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader wrote meter", err)
	}
	if _, err = f.app.Store.EnergyOverview(f.ctx, reader, ""); err != nil {
		t.Fatal("reader overview denied", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.EnergyOverview(f.ctx, foreign, ""); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("foreign household overview", err)
	}
	var photo bytes.Buffer
	if err = png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	photoKey := value.UUID()
	endpoint := "/api/v1/meters/readings/" + store.String(reading["id"]) + "/photo"
	upload := func(data []byte, revision string) (int, []byte) {
		t.Helper()
		var payload bytes.Buffer
		form := multipart.NewWriter(&payload)
		for name, val := range map[string]string{"generation": fmt.Sprint(state.Generation), "reading_revision": revision, "capture": "false"} {
			if err := form.WriteField(name, val); err != nil {
				t.Fatal(err)
			}
		}
		part, err := form.CreateFormFile("file", "actual-meter.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(data); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		response, raw, err := f.request(http.MethodPost, endpoint, f.token, csrf, form.FormDataContentType(), &payload, map[string]string{"Idempotency-Key": photoKey})
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	status, proof := upload(photo.Bytes(), "1")
	if status != 201 {
		t.Fatal("actual sanitized photo not committed", status, string(proof))
	}
	status, replay := upload(photo.Bytes(), "1")
	if status != 200 || !bytes.Equal(bytes.TrimSpace(proof), bytes.TrimSpace(replay)) {
		t.Fatal("same photo lost-response retry", status, string(replay))
	}
	var attachments int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.attachments WHERE module_id='meters' AND resource_id=$1`, reading["id"]).Scan(&attachments); err != nil || attachments != 1 {
		t.Fatal("photo replay duplicated storage", attachments, err)
	}
	status, _ = upload([]byte("%PDF-1.4\nactual test"), "1")
	if status != 422 {
		t.Fatal("non-photo accepted", status)
	}
	photoKey = value.UUID()
	status, _ = upload(photo.Bytes(), "2")
	if status != 409 {
		t.Fatal("stale reading photo version accepted", status)
	}
}
