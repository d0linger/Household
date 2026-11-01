package web

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"household/internal/store"
	"household/internal/value"
)

func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	header := []string{}
	withPantrySources := m.ID == "pantry" && (e.ID == "products" || e.ID == "packs" || e.ID == "barcodes")
	for _, f := range e.Fields {
		header = append(header, f.Name)
	}
	if withPantrySources {
		header = append(header, "source_provenance")
	}
	if err = writer.Write(header); err != nil {
		s.fail(w, r, err)
		return
	}
	emit := func(rows []store.Record) error {
		var sources map[string]string
		if withPantrySources && len(rows) != 0 {
			ids := make([]string, 0, len(rows))
			for _, row := range rows {
				ids = append(ids, store.String(row["id"]))
			}
			var sourceErr error
			sources, sourceErr = s.Store.PantryFoodFactsExportSources(r.Context(), identity(r), e.ID, ids)
			if sourceErr != nil {
				return sourceErr
			}
		}
		for _, row := range rows {
			line := make([]string, 0, len(e.Fields))
			for _, f := range e.Fields {
				v := formValue(f, row[f.Name])
				if f.Kind == "text" || f.Kind == "longtext" {
					v = csvSafe(v)
				}
				line = append(line, v)
			}
			if withPantrySources {
				provenance := sources[store.String(row["id"])]
				if provenance == "" {
					provenance = "[]"
				}
				line = append(line, provenance)
			}
			if err := writer.Write(line); err != nil {
				return err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		if (m.ID == "expenses" || withPantrySources) && buf.Len() > 16<<20 {
			return store.Invalid{Message: "Export überschreitet die unterstützte Ausgabegröße; Betreiber-Sicherung verwenden"}
		}
		return nil
	}
	if m.ID == "expenses" && (e.ID == "entries" || e.ID == "entry_lines") {
		err = s.Store.ExportFinancial(r.Context(), identity(r), e.ID, emit)
	} else {
		for offset := 0; offset < 10000; offset += 100 {
			var rows []store.Record
			rows, err = s.Store.List(r.Context(), identity(r), m, e, "", 100, offset)
			if err != nil {
				break
			}
			if err = emit(rows); err != nil {
				break
			}
			if len(rows) < 100 {
				break
			}
			if offset == 9900 {
				err = store.Invalid{Message: "Export umfasst mehr als 10.000 Einträge; bitte über die Betreiber-Sicherung exportieren"}
			}
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, m.ID, e.ID))
	s.writeBytes(w, r, buf.Bytes())
}
func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
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
	file, _, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "CSV-Datei erforderlich"})
		return
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		s.fail(w, r, store.Invalid{Message: "CSV-Datei maximal 1 MiB"})
		return
	}
	raw = bytes.TrimPrefix(raw, []byte{239, 187, 191})
	reader := csv.NewReader(bytes.NewReader(raw))
	first := strings.SplitN(string(raw), "\n", 2)[0]
	if strings.Count(first, ";") > strings.Count(first, ",") {
		reader.Comma = ';'
	}
	header, err := reader.Read()
	if err != nil || len(header) > 50 {
		s.fail(w, r, store.Invalid{Message: "Ungültige CSV-Kopfzeile"})
		return
	}
	allowed := map[string]bool{}
	pantrySources := m.ID == "pantry" && (e.ID == "products" || e.ID == "packs" || e.ID == "barcodes")
	for _, f := range e.Fields {
		allowed[f.Name] = true
	}
	if pantrySources {
		allowed["source_provenance"] = true
	}
	seen := map[string]bool{}
	for _, h := range header {
		if !allowed[h] || seen[h] {
			s.fail(w, r, store.Invalid{Message: "Unbekannter oder doppelter Feldname in Kopfzeile"})
			return
		}
		seen[h] = true
	}
	type rowData struct {
		raw     []byte
		message string
	}
	items := []rowData{}
	preview := []store.Record{}
	failures := 0
	for n := 1; ; n++ {
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || n > 500 {
			s.fail(w, r, store.Invalid{Message: "Ungültige CSV-Zeilen oder mehr als 500 Datensätze"})
			return
		}
		data := map[string]json.RawMessage{}
		sourceMessage := ""
		for i, v := range line {
			if pantrySources && header[i] == "source_provenance" {
				// A CSV cannot create trusted provider provenance. Preserve an
				// attributed export as evidence; fresh adoption has its own review.
				if strings.TrimSpace(v) != "" && strings.TrimSpace(v) != "[]" {
					sourceMessage = "Herkunftsmetadaten sind ein schreibgeschützter Quellenauszug. Datei als Nachweis bewahren; Produktdaten über die Quellenprüfung übernehmen."
				}
				continue
			}
			b, _ := json.Marshal(v)
			data[header[i]] = b
		}
		_, validation := e.Normalize(data, false)
		message := ""
		if sourceMessage != "" {
			message = sourceMessage
			failures++
		} else if validation != nil {
			message = validation.Error()
			failures++
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		items = append(items, rowData{encoded, message})
		if n <= 50 {
			preview = append(preview, store.Record{"row": n + 1, "values": string(encoded), "error": message})
		}
	}
	if len(items) == 0 {
		s.fail(w, r, store.Invalid{Message: "Keine Datensätze gefunden"})
		return
	}
	state, _ := s.Store.State(m.ID)
	tx, err := s.Store.BeginMutation(r.Context(), identity(r), m.ID, state.Generation)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	batch := value.UUID()
	financeSources := []store.FinanceResourceRef{}
	if m.ID == "expenses" && e.ID == "entry_lines" {
		for _, item := range items {
			var fields map[string]json.RawMessage
			var parent string
			if json.Unmarshal(item.raw, &fields) == nil && json.Unmarshal(fields["entry_id"], &parent) == nil && value.ValidUUID(parent) {
				financeSources = append(financeSources, store.FinanceResourceRef{Kind: "entries", ID: parent})
			}
		}
	}
	financeOperation, err := s.Store.PrepareFinanceImport(r.Context(), tx, identity(r), m.ID, batch, financeSources)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO core.import_batches(id,household_id,module_id,module_generation,entity,row_count,valid_count,error_count,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", batch, identity(r).HouseholdID, m.ID, state.Generation, e.ID, len(items), len(items)-failures, failures, identity(r).UserID)
	for n, x := range items {
		if err != nil {
			break
		}
		messages, _ := json.Marshal([]string{})
		if x.message != "" {
			messages, _ = json.Marshal([]string{x.message})
		}
		_, err = tx.Exec(r.Context(), "INSERT INTO core.import_rows(batch_id,row_number,normalized_values,validation_errors) VALUES($1,$2,$3,$4)", batch, n+1, x.raw, messages)
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
	p := s.page(r, "Import prüfen")
	p.View = "import"
	p.Records = preview
	p.Extra = map[string]any{"count": len(items), "valid": len(items) - failures, "errors": failures, "confirm_url": "/api/v1/core/imports/" + m.ID + "/" + e.ID + "/" + batch + "/confirm"}
	s.render(w, r, p)
}
func (s *Server) confirmImport(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, e, err := s.resolve(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	state, _ := s.Store.State(m.ID)
	if err = s.Store.ConfirmImport(r.Context(), identity(r), m, e, r.PathValue("id"), trace(r), state.Generation); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/m/"+m.ID+"/"+e.ID, http.StatusSeeOther)
}
