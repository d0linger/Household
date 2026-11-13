package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"household/internal/store"
	"household/internal/value"
)

var financeWorkspacePanes = []string{"overview", "entries", "accounts", "planning", "people", "import", "reports"}

func (s *Server) registerFinanceRoutes(mux *http.ServeMux) {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /m/expenses/workspace/{pane}":                                s.financeWorkspacePage,
		"GET /api/v1/expenses/finance/view":                               s.financeViewAPI,
		"GET /api/v1/expenses/finance/entries/{id}/payment-reminder":      s.financePaymentReminderAPI,
		"POST /api/v1/expenses/finance/entries/{id}/payment-reminder":     s.financePaymentReminderSaveAPI,
		"POST /api/v1/expenses/finance/preview":                           s.financePreviewAPI,
		"POST /api/v1/expenses/finance/apply":                             s.financeApplyAPI,
		"GET /api/v1/expenses/finance/operations/{key}":                   s.financeOperationAPI,
		"POST /api/v1/expenses/finance/operations/{key}/seal":             s.financeSealAPI,
		"GET /api/v1/expenses/finance/references/{kind}":                  s.financeReferencesAPI,
		"GET /api/v1/expenses/finance/access/{kind}/{id}":                 s.financeAccessInfoAPI,
		"GET /api/v1/expenses/finance/export":                             s.financeExportAPI,
		"POST /api/v1/expenses/finance/bank/previews":                     s.financeBankCreateAPI,
		"GET /api/v1/expenses/finance/bank/previews/{id}":                 s.financeBankReadAPI,
		"POST /api/v1/expenses/finance/bank/previews/{id}/confirm":        s.financeBankConfirmAPI,
		"GET /api/v1/expenses/finance/receipts/{attachment}/pages/{page}": s.financeReceiptPageAPI,
		"POST /api/v1/expenses/finance/receipts/{attachment}/recognize":   s.financeReceiptRecognizeAPI,
	} {
		mux.Handle(pattern, s.require(handler))
	}
}

func (s *Server) financeWorkspacePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	pane := r.PathValue("pane")
	if !slices.Contains(financeWorkspacePanes, pane) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	initial, err := s.Store.FinanceView(r.Context(), identity(r), store.FinanceQuery{Pane: "overview"})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Finanzen")
	p.Module, _ = s.Store.Module("expenses")
	p.View = "finance-workspace"
	p.Extra = store.Record{"pane": pane, "generation": initial["generation"], "access_version": initial["access_version"]}
	s.render(w, r, p)
}

func financeQuery(r *http.Request) (store.FinanceQuery, error) {
	var q store.FinanceQuery
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 12000 {
		return q, store.Invalid{Message: "Finanzfilter zu lang oder ungültig"}
	}
	allowed := []string{"pane", "section", "id", "goal", "month", "from", "to", "account", "person", "category", "payee", "state", "cursor"}
	for key, entries := range values {
		if !slices.Contains(allowed, key) || len(entries) != 1 {
			return q, store.Invalid{Message: "Unbekannte oder doppelte Finanzauswahl"}
		}
	}
	q = store.FinanceQuery{Pane: values.Get("pane"), Section: values.Get("section"), ID: values.Get("id"), Month: values.Get("month"),
		Goal: values.Get("goal"),
		From: values.Get("from"), To: values.Get("to"), Account: values.Get("account"), Person: values.Get("person"),
		Category: values.Get("category"), Payee: values.Get("payee"), State: values.Get("state"), Cursor: values.Get("cursor")}
	return q, nil
}

func (s *Server) financeViewAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	q, err := financeQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	bindingQuery := q
	bindingQuery.Cursor = ""
	if q.Month == "" && q.Cursor != "" {
		if month := financeUnverifiedCursorMonth(q.Cursor); month != "" {
			q.Month = month
			bindingQuery.Month = month
		}
	}
	binding, _ := json.Marshal(bindingQuery)
	q.Cursor, err = s.financeReadCursor(r, q.Cursor, "view:"+string(binding))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Store.FinanceView(r.Context(), identity(r), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if q.Pane == "import" && q.Section == "receipts" && q.ID != "" {
		if err = s.enrichFinanceReceiptMetadata(r, out); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if next := store.String(out["next_cursor"]); next != "" {
		nextQuery := bindingQuery
		if nextQuery.Month == "" {
			nextQuery.Month = store.String(out["month"])
		}
		nextBinding, _ := json.Marshal(nextQuery)
		out["next_cursor"] = s.financeSignCursorMonth(r, next, "view:"+string(nextBinding), nextQuery.Month)
	}
	if detail, ok := out["detail"].(store.Record); ok {
		if next := store.String(detail["next_cursor"]); next != "" {
			nextQuery := bindingQuery
			if nextQuery.Month == "" {
				nextQuery.Month = store.String(out["month"])
			}
			nextBinding, _ := json.Marshal(nextQuery)
			detail["next_cursor"] = s.financeSignCursorMonth(r, next, "view:"+string(nextBinding), nextQuery.Month)
		}
		if next := store.String(detail["contributions_next_cursor"]); next != "" {
			record, _ := detail["record"].(store.Record)
			follow := store.FinanceQuery{Pane: "planning", Section: "contributions", Goal: store.String(record["id"])}
			followBinding, _ := json.Marshal(follow)
			detail["contributions_next_cursor"] = s.financeSignCursor(r, next, "view:"+string(followBinding))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func financeScope(r *http.Request) string {
	id := identity(r)
	raw, _ := json.Marshal([]any{"finance-review-v1", id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.Role, id.AuthVersion})
	return string(raw)
}

type financeReviewEnvelope struct {
	Version    int    `json:"version"`
	Key        string `json:"key"`
	Hash       string `json:"hash"`
	ImpactHash string `json:"impact_hash"`
	Expires    int64  `json:"expires"`
}

func financeJSON(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		return nil, store.Invalid{Message: "Genau einen JSON-Finanzvorgang ohne URL-Parameter senden"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, store.Invalid{Message: "Finanzangaben überschreiten die erlaubte Größe"}
	}
	return raw, nil
}

func (s *Server) financePreviewAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	raw, err := financeJSON(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	command, err := store.DecodeFinanceCommand(raw)
	if err != nil || command.ReviewToken != "" {
		if err == nil {
			err = store.Invalid{Message: "Eine neue Vorschau ohne alten Prüfbeleg anfordern"}
		}
		s.fail(w, r, err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	out, err := s.Store.FinancePreview(r.Context(), identity(r), key, trace(r), command)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	checked, ok := out["command"].(store.FinanceCommand)
	if !ok {
		s.fail(w, r, store.ErrConflict)
		return
	}
	hash, err := store.FinanceCommandHash(checked)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	expires := time.Now().Add(5 * time.Minute).Unix()
	envelope, err := json.Marshal(financeReviewEnvelope{Version: 1, Key: key, Hash: hash, ImpactHash: store.String(out["impact_hash"]), Expires: expires})
	if err != nil || len(store.String(out["impact_hash"])) != 64 {
		s.fail(w, r, store.ErrConflict)
		return
	}
	payload := base64.RawURLEncoding.EncodeToString(envelope)
	out["review_token"], out["expires_at"] = payload+"."+s.Auth.CSRF(payload, financeScope(r)), expires
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeApplyAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	raw, err := financeJSON(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	command, err := store.DecodeFinanceCommand(raw)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	invalid := store.Invalid{Message: "Prüfbeleg ist ungültig oder abgelaufen. Den Vorgang mit demselben Schlüssel erneut prüfen."}
	payload, signature, ok := strings.Cut(command.ReviewToken, ".")
	if !ok || !s.Auth.ValidCSRF(payload, financeScope(r), signature) {
		s.fail(w, r, invalid)
		return
	}
	envelopeBytes, err := base64.RawURLEncoding.DecodeString(payload)
	var envelope financeReviewEnvelope
	if err != nil || store.DecodeFinanceDocument(envelopeBytes, &envelope) != nil || envelope.Version != 1 ||
		envelope.Expires <= 0 || envelope.Expires > time.Now().Add(5*time.Minute+5*time.Second).Unix() ||
		!value.ValidUUID(envelope.Key) || envelope.Key != r.Header.Get("Idempotency-Key") || len(envelope.ImpactHash) != 64 {
		s.fail(w, r, invalid)
		return
	}
	hash, err := store.FinanceCommandHash(command)
	if err != nil || hash != envelope.Hash {
		s.fail(w, r, invalid)
		return
	}
	command.ReviewHash = envelope.ImpactHash
	var out store.Record
	if envelope.Expires <= time.Now().Unix() {
		out, err = s.Store.FinanceReplay(r.Context(), identity(r), envelope.Key, command)
	} else {
		out, err = s.Store.FinanceApply(r.Context(), identity(r), envelope.Key, trace(r), command)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeOperationAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if r.URL.RawQuery != "" {
		s.fail(w, r, store.Invalid{Message: "Vorgangsstatus ohne zusätzliche Filter abrufen"})
		return
	}
	out, err := s.Store.FinanceOperation(r.Context(), identity(r), r.PathValue("key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeSealAPI(w http.ResponseWriter, r *http.Request) {
	raw, err := financeJSON(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var input struct {
		Generation int64 `json:"generation"`
	}
	if err = store.DecodeFinanceDocument(raw, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.Store.FinanceSealOperation(r.Context(), identity(r), r.PathValue("key"), trace(r), input.Generation); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, store.Record{"state": "sealed", "effects": "unknown"})
}

type financeCursorEnvelope struct {
	Raw     string `json:"raw"`
	Expires int64  `json:"expires"`
	Month   string `json:"month,omitempty"`
}

func (s *Server) financeSignCursor(r *http.Request, raw, binding string) string {
	return s.financeSignCursorMonth(r, raw, binding, "")
}

func (s *Server) financeSignCursorMonth(r *http.Request, raw, binding, month string) string {
	payloadBytes, err := json.Marshal(financeCursorEnvelope{Raw: raw, Expires: time.Now().Add(30 * time.Minute).Unix(), Month: month})
	if err != nil {
		return ""
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return payload + "." + s.Auth.CSRF(payload, "finance-cursor:"+financeScope(r)+binding)
}

// The month is untrusted here and only selects the candidate signing scope.
// financeReadCursor must authenticate the entire envelope before it is used.
func financeUnverifiedCursorMonth(token string) string {
	if len(token) > 4096 {
		return ""
	}
	payload, _, ok := strings.Cut(token, ".")
	if !ok {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	var envelope financeCursorEnvelope
	if err != nil || store.DecodeFinanceDocument(raw, &envelope) != nil || len(envelope.Month) != 7 {
		return ""
	}
	month, err := time.Parse("2006-01", envelope.Month)
	if err != nil || month.Year() < 1900 || month.Year() > 2200 {
		return ""
	}
	return envelope.Month
}

func (s *Server) financeReadCursor(r *http.Request, token, binding string) (string, error) {
	if token == "" {
		return "", nil
	}
	invalid := store.Invalid{Message: "Die Seite ist abgelaufen. Auswahl erneut laden."}
	if len(token) > 4096 {
		return "", invalid
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || !s.Auth.ValidCSRF(payload, "finance-cursor:"+financeScope(r)+binding, signature) {
		return "", invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	var envelope financeCursorEnvelope
	if err != nil || store.DecodeFinanceDocument(raw, &envelope) != nil || envelope.Expires <= time.Now().Unix() || len(envelope.Raw) > 1024 {
		return "", invalid
	}
	return envelope.Raw, nil
}

func (s *Server) financeReferencesAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 6000 {
		s.fail(w, r, store.Invalid{Message: "Referenzsuche erneut prüfen"})
		return
	}
	for key, entries := range values {
		if !slices.Contains([]string{"q", "selected", "cursor"}, key) || len(entries) != 1 {
			s.fail(w, r, store.Invalid{Message: "Unbekannte oder doppelte Referenzauswahl"})
			return
		}
	}
	query := store.FinanceReferenceQuery{Kind: r.PathValue("kind"), Query: values.Get("q"), Selected: values.Get("selected")}
	binding, _ := json.Marshal(query)
	query.Cursor, err = s.financeReadCursor(r, values.Get("cursor"), "refs:"+string(binding))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Store.FinanceReferences(r.Context(), identity(r), query)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if next := store.String(out["next_cursor"]); next != "" {
		out["next_cursor"] = s.financeSignCursor(r, next, "refs:"+string(binding))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeAccessInfoAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if r.URL.RawQuery != "" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	out, err := s.Store.FinanceAccessInfo(r.Context(), identity(r), r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeExportAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	q, err := financeQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if q.Cursor != "" {
		s.fail(w, r, store.Invalid{Message: "Export verwendet die gesamte Auswahl ohne Seitenzeiger"})
		return
	}
	raw, err := s.Store.FinanceExport(r.Context(), identity(r), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="household-finanzen.csv"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, bytes.NewReader(raw))
}

func (s *Server) financeBankCreateAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 3 || len(values["account"]) != 1 || len(values["profile"]) != 1 || len(values["generation"]) != 1 {
		s.fail(w, r, store.Invalid{Message: "Konto, Profil und Generation für die Bankdatei auswählen"})
		return
	}
	generation, err := strconv.ParseInt(values.Get("generation"), 10, 64)
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaErr != nil || generation <= 0 || media != "text/csv" {
		s.fail(w, r, store.Invalid{Message: "Begrenzte UTF-8-CSV mit gültiger Generation senden"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "Bankdatei ist größer als1MiB"})
		return
	}
	out, err := s.Store.FinanceBankPreview(r.Context(), identity(r), r.Header.Get("Idempotency-Key"), trace(r), values.Get("account"), values.Get("profile"), generation, raw)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeBankReadAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if r.URL.RawQuery != "" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	out, err := s.Store.FinanceBankReview(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) financeBankConfirmAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	raw, err := financeJSON(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var input store.FinanceBankConfirmInput
	if err = store.DecodeFinanceDocument(raw, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Store.FinanceBankConfirm(r.Context(), identity(r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
