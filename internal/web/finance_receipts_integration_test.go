//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func financeReceiptHTTPFixture(t *testing.T) (*integrationFixture, string, string) {
	t.Helper()
	f, _, entry, _ := expenseIntegrityFixture(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, text := range map[string]string{"csrf": f.app.Auth.CSRF(f.token, "session"), "generation": strconv.FormatInt(f.app.Store.States()["expenses"].Generation, 10), "capture": "false"} {
		if err := form.WriteField(key, text); err != nil {
			t.Fatal(err)
		}
	}
	file, err := form.CreateFormFile("file", "finance-receipt-private-sentinel.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(financeReceiptTestImage(t)); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodPost, "/api/v1/m/expenses/entries/"+store.String(entry["id"])+"/attachments", f.token, f.app.Auth.CSRF(f.token, "session"), form.FormDataContentType(), &body, nil)
	if err != nil || response.StatusCode != http.StatusSeeOther {
		t.Fatal("actual authorized attachment upload failed", err)
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/m/expenses/entries/"+store.String(entry["id"])+"/attachments", f.token, "", "", nil, nil)
	var attachments []store.Record
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &attachments) != nil || len(attachments) != 1 {
		t.Fatal("actual uploaded attachment list is not exact")
	}
	return f, store.String(entry["id"]), store.String(attachments[0]["id"])
}

func TestFinanceReceiptProtectedRasterAndReaderView(t *testing.T) {
	f, _, attachment := financeReceiptHTTPFixture(t)
	reader := shoppingTestMember(t, f, "finance-receipt-reader", "reader")
	token, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "receipt-reader")
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []struct{ name, token string }{{"writer", f.token}, {"reader", token}} {
		t.Run(actor.name, func(t *testing.T) {
			response, raw, err := f.request(http.MethodGet, "/api/v1/expenses/finance/receipts/"+attachment+"/pages/1", actor.token, "", "", nil, nil)
			config, format, decodeErr := image.DecodeConfig(bytes.NewReader(raw))
			if err != nil || response.StatusCode != http.StatusOK || decodeErr != nil || config.Width != 2 || config.Height != 3 || format != "png" || response.Header.Get("Content-Type") != "image/png" || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") || response.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("current-authorized private raster contract failed")
			}
			response, raw, err = f.request(http.MethodGet, "/api/v1/expenses/finance/view?pane=import&section=receipts&id="+attachment, actor.token, "", "", nil, nil)
			var view struct {
				Detail struct {
					PageCount  *int           `json:"page_count"`
					Pages      []store.Record `json:"pages"`
					OCR        bool           `json:"ocr_available"`
					Attachment store.Record   `json:"attachment"`
				} `json:"detail"`
			}
			if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &view) != nil || view.Detail.PageCount == nil || *view.Detail.PageCount != 1 || len(view.Detail.Pages) != 1 || view.Detail.OCR || store.String(view.Detail.Attachment["id"]) != attachment || bytes.Contains(raw, []byte("storage_key")) {
				t.Fatal("metadata invented capabilities/pages or exposed storage identity")
			}
		})
	}
	for _, target := range []struct {
		name, path, token string
		status            int
	}{
		{"anonymous", "/pages/1", "", http.StatusUnauthorized},
		{"no_second_image_page", "/pages/2", f.token, http.StatusUnprocessableEntity},
		{"noncanonical_page", "/pages/01", f.token, http.StatusNotFound},
		{"unrecognized_query", "/pages/1?download=true", f.token, http.StatusNotFound},
	} {
		t.Run(target.name, func(t *testing.T) {
			response, raw, err := f.request(http.MethodGet, "/api/v1/expenses/finance/receipts/"+attachment+target.path, target.token, "", "", nil, nil)
			if err != nil || response.StatusCode != target.status || bytes.HasPrefix(raw, []byte("\x89PNG")) || bytes.Contains(raw, []byte("finance-receipt-private-sentinel")) {
				t.Fatal("denied raster disclosed original source")
			}
		})
	}
}

func TestFinanceReceiptRecognizeStrictAuthorityAndNoFactEffects(t *testing.T) {
	f, _, attachment := financeReceiptHTTPFixture(t)
	source, err := f.app.Store.FinanceReceiptSource(f.ctx, f.identity, attachment)
	if err != nil {
		t.Fatal(err)
	}
	reader := shoppingTestMember(t, f, "finance-receipt-ocr-reader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "receipt-ocr-reader")
	if err != nil {
		t.Fatal(err)
	}
	input := store.FinanceReceiptRecognizeInput{Generation: source.Generation, SourceVersion: source.SourceVersion, SourceHash: source.SHA256, Currency: "EUR", Exponent: 2, Pages: []int{1}, DecimalSeparator: ","}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	counts := func() string {
		t.Helper()
		var state string
		if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_array((SELECT count(*) FROM core.capture_jobs),(SELECT count(*) FROM expenses.entries),(SELECT count(*) FROM expenses.receipt_reviews))::text`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := counts()
	for _, test := range []struct {
		name, token, csrf, body string
		enabled                 bool
		status                  int
	}{
		{"disabled", f.token, f.app.Auth.CSRF(f.token, "session"), string(raw), false, http.StatusUnprocessableEntity},
		{"reader", readerToken, f.app.Auth.CSRF(readerToken, "session"), string(raw), true, http.StatusForbidden},
		{"no_csrf", f.token, "invalid", string(raw), true, http.StatusForbidden},
		{"unknown_field", f.token, f.app.Auth.CSRF(f.token, "session"), strings.TrimSuffix(string(raw), "}") + `,"ocr_text":"forged"}`, true, http.StatusUnprocessableEntity},
		{"too_many_pages", f.token, f.app.Auth.CSRF(f.token, "session"), strings.Replace(string(raw), `"pages":[1]`, `"pages":[1,2,3,4]`, 1), true, http.StatusUnprocessableEntity},
		{"old_source", f.token, f.app.Auth.CSRF(f.token, "session"), strings.Replace(string(raw), source.SHA256, strings.Repeat("0", 64), 1), true, http.StatusUnprocessableEntity},
		{"bounded_body", f.token, f.app.Auth.CSRF(f.token, "session"), strings.Repeat(" ", 8193) + string(raw), true, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.app.Cfg.OCREnabled = test.enabled
			response, body, err := f.request(http.MethodPost, "/api/v1/expenses/finance/receipts/"+attachment+"/recognize", test.token, test.csrf, "application/json", strings.NewReader(test.body), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || response.StatusCode != test.status || counts() != before || bytes.Contains(body, []byte("finance-receipt-private-sentinel")) {
				t.Fatal("denied recognition changed facts or disclosed source")
			}
		})
	}
}

func TestFinanceReceiptCurrentSourceRevocationAndSaveFence(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{"retired_financial_source", "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1"},
		{"disabled_module", "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='expenses'"},
		{"orphaned_attachment", "UPDATE core.attachments SET state='orphaned' WHERE id=$1"},
		{"old_attachment_generation", "UPDATE core.attachments SET module_generation=module_generation+1 WHERE id=$1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, entry, attachment := financeReceiptHTTPFixture(t)
			source, err := f.app.Store.FinanceReceiptSource(f.ctx, f.identity, attachment)
			if err != nil {
				t.Fatal(err)
			}
			arg := attachment
			switch test.name {
			case "retired_financial_source":
				arg = entry
			case "disabled_module":
				arg = f.identity.HouseholdID
			}
			if _, err = f.owner.Exec(f.ctx, test.statement, arg); err != nil {
				t.Fatal(err)
			}
			response, body, err := f.request(http.MethodGet, "/api/v1/expenses/finance/receipts/"+attachment+"/pages/1", f.token, "", "", nil, nil)
			if err != nil || response.StatusCode != http.StatusNotFound || bytes.HasPrefix(body, []byte("\x89PNG")) || bytes.Contains(body, []byte("finance-receipt-private-sentinel")) {
				t.Fatal("source withdrawal did not close raster disclosure")
			}
			input := store.FinanceReceiptRecognizeInput{Generation: source.Generation, SourceVersion: source.SourceVersion, SourceHash: source.SHA256, Currency: "EUR", Exponent: 2, Pages: []int{1}, DecimalSeparator: ","}
			_, err = f.app.Store.FinanceSaveReceiptSuggestions(f.ctx, f.identity, value.UUID(), value.UUID(), source, input, store.FinanceReceiptSuggestions{RecognizedPages: []int{1}, Lines: []store.FinanceReceiptLine{}, TotalCandidates: []store.FinanceReceiptTotalCandidate{}})
			if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrConflict) {
				t.Fatal("withdrawn source accepted stale OCR evidence")
			}
			var count int
			if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.capture_jobs WHERE source_attachment_id=$1", attachment).Scan(&count); err != nil || count != 0 {
				t.Fatal("stale OCR save created capture metadata")
			}
			foreign := f.identity
			foreign.HouseholdID = value.UUID()
			if _, err = f.app.Store.FinanceReceiptSource(f.ctx, foreign, attachment); !errors.Is(err, store.ErrForbidden) && !errors.Is(err, store.ErrNotFound) {
				t.Fatal("foreign scope read original receipt source")
			}
		})
	}
}
