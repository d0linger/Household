package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/finance"
	"household/internal/store"
	"household/internal/value"
)

const financeReceiptMaxBytes = 10 << 20

type financeReceiptBuffer struct {
	bytes.Buffer
	limit int
}

func (b *financeReceiptBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("receipt process output limit")
	}
	return b.Buffer.Write(p)
}

func financeReceiptLocal(ctx context.Context, input []byte, binary string, args ...string) ([]byte, error) {
	if binary != "/usr/bin/pdfinfo" && binary != "/usr/bin/pdftoppm" && binary != "/usr/bin/tesseract" {
		return nil, errors.New("unsupported receipt processor")
	}
	// Security: only three fixed local executables, fixed flags and bounded decimal
	// page numbers reach this sink. No filename, shell, URL or operator command.
	cmd := exec.CommandContext(ctx, binary, args...) // #nosec G204 -- compiled executable allowlist above; argv assembled only by the private bounded render/OCR helpers below
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "OMP_THREAD_LIMIT=2", "OMP_NUM_THREADS=2"}
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = time.Second
	limit := 16 << 20
	switch binary {
	case "/usr/bin/tesseract":
		limit = 512 << 10
	case "/usr/bin/pdfinfo":
		limit = 16 << 10
	}
	output, diagnostic := &financeReceiptBuffer{limit: limit}, &financeReceiptBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = output, diagnostic
	if err := cmd.Run(); err != nil {
		// Tool diagnostics can contain original receipt text and never cross HTTP
		// or log boundaries. The caller supplies a fixed public error instead.
		return nil, errors.New("local receipt processor unavailable or failed")
	}
	return bytes.Clone(output.Bytes()), nil
}

func financeReceiptFile(directory string, source store.FinanceReceiptSource) ([]byte, error) {
	if !value.ValidUUID(source.StorageKey) || source.SizeBytes < 1 || source.SizeBytes > financeReceiptMaxBytes ||
		len(source.SHA256) != 64 || strings.ToLower(source.SHA256) != source.SHA256 ||
		!slices.Contains([]string{"image/jpeg", "image/png", "application/pdf"}, source.MediaType) {
		return nil, store.ErrNotFound
	}
	expected, err := hex.DecodeString(source.SHA256)
	if err != nil || len(expected) != sha256.Size {
		return nil, store.ErrNotFound
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, store.ErrNotFound
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(source.StorageKey)
	if err != nil || !info.Mode().IsRegular() || info.Size() != source.SizeBytes {
		return nil, store.ErrNotFound
	}
	file, err := root.Open(source.StorageKey)
	if err != nil {
		return nil, store.ErrNotFound
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != source.SizeBytes {
		return nil, store.ErrNotFound
	}
	raw, err := io.ReadAll(io.LimitReader(file, financeReceiptMaxBytes+1))
	digest := sha256.Sum256(raw)
	if err != nil || int64(len(raw)) != source.SizeBytes || !bytes.Equal(digest[:], expected) {
		return nil, store.ErrConflict
	}
	if source.MediaType == "application/pdf" {
		if !bytes.HasPrefix(raw, []byte("%PDF-")) {
			return nil, store.ErrNotFound
		}
	} else if _, err = financeReceiptImage(raw, source.MediaType); err != nil {
		return nil, err
	}
	return raw, nil
}

func financeReceiptImage(raw []byte, media string) (image.Config, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 20_000_000/config.Height ||
		(media == "image/png" && format != "png") || (media == "image/jpeg" && format != "jpeg") ||
		(media != "image/png" && media != "image/jpeg") {
		return image.Config{}, store.Invalid{Message: "Belegbild ist ungültig oder größer als20Megapixel"}
	}
	return config, nil
}

func financeReceiptRaster(ctx context.Context, raw []byte, media string, page int) ([]byte, error) {
	if page < 1 || page > 20 {
		return nil, store.ErrNotFound
	}
	if media == "application/pdf" {
		var err error
		raw, err = financeReceiptLocal(ctx, raw, "/usr/bin/pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-scale-to", "3000", "-png", "-")
		if err != nil {
			return nil, store.Invalid{Message: "Diese PDF-Seite kann lokal derzeit nicht dargestellt werden"}
		}
		media = "image/png"
	} else if page != 1 {
		return nil, store.ErrNotFound
	}
	config, err := financeReceiptImage(raw, media)
	if err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, store.Invalid{Message: "Belegbild kann nicht vollständig gelesen werden"}
	}
	width, height := config.Width, config.Height
	if width > 3000 || height > 3000 {
		if width >= height {
			height = max(1, height*3000/width)
			width = 3000
		} else {
			width = max(1, width*3000/height)
			height = 3000
		}
	}
	// Re-encode pixels without original metadata; the response is always PNG.
	raster := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := range width {
			raster.Set(x, y, decoded.At(decoded.Bounds().Min.X+x*config.Width/width, decoded.Bounds().Min.Y+y*config.Height/height))
		}
	}
	output := &financeReceiptBuffer{limit: 16 << 20}
	if err = png.Encode(output, raster); err != nil {
		return nil, store.Invalid{Message: "Belegvorschau überschreitet die erlaubte Größe"}
	}
	return bytes.Clone(output.Bytes()), nil
}

func financeReceiptPageCount(raw []byte) (*int, error) {
	var found *int
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, field, ok := strings.Cut(line, ":")
		if !ok || key != "Pages" {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || count < 1 || count > 1_000_000 || found != nil {
			return nil, errors.New("invalid receipt page count")
		}
		found = &count
	}
	if found == nil {
		return nil, errors.New("receipt page count unknown")
	}
	return found, nil
}

func financeReceiptKnownPages(ctx context.Context, source store.FinanceReceiptSource, raw []byte) (*int, error) {
	if source.MediaType != "application/pdf" {
		one := 1
		return &one, nil
	}
	info, err := financeReceiptLocal(ctx, raw, "/usr/bin/pdfinfo", "-")
	if err != nil {
		return nil, err
	}
	return financeReceiptPageCount(info)
}

func financeReceiptTool(path string) bool {
	_, err := exec.LookPath(path)
	return err == nil
}

func (s *Server) enrichFinanceReceiptMetadata(r *http.Request, result store.Record) error {
	if result["detail"] == nil && r.URL.Query().Get("id") == "" {
		// The bounded authorized attachment list has no individual source to
		// inspect. Local file/CPU work starts only after an explicit selection.
		return nil
	}
	detail, ok := result["detail"].(store.Record)
	if !ok {
		if data, valid := result["detail"].(map[string]any); valid {
			detail, ok = store.Record(data), true
		}
	}
	if !ok {
		return store.ErrNotFound
	}
	attachment, ok := detail["attachment"].(store.Record)
	if !ok {
		if data, valid := detail["attachment"].(map[string]any); valid {
			attachment, ok = store.Record(data), true
		}
	}
	if !ok {
		return store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	case <-ctx.Done():
		return store.ErrConflict
	}
	source, err := s.Store.FinanceReceiptSource(ctx, identity(r), store.String(attachment["id"]))
	if err != nil {
		return err
	}
	raw, err := financeReceiptFile(s.Cfg.UploadDir, source)
	if err != nil {
		return err
	}
	count, countErr := financeReceiptKnownPages(ctx, source, raw)
	latest, err := s.Store.FinanceReceiptSource(ctx, identity(r), source.AttachmentID)
	if err != nil {
		return err
	}
	if source != latest {
		return store.ErrConflict
	}
	pdf := source.MediaType == "application/pdf"
	renderer := !pdf || financeReceiptTool("/usr/bin/pdftoppm")
	detail["pdf_preview_available"] = pdf && renderer && countErr == nil
	detail["ocr_available"] = source.CanReview && s.Cfg.OCREnabled && financeReceiptTool("/usr/bin/tesseract") && renderer && countErr == nil
	detail["page_count"] = count
	pages := []store.Record{}
	if countErr == nil {
		for number := 1; number <= min(*count, 20); number++ {
			pages = append(pages, store.Record{"number": number, "url": "/api/v1/expenses/finance/receipts/" + source.AttachmentID + "/pages/" + strconv.Itoa(number), "available": renderer, "reason": ""})
		}
		if *count > 20 {
			detail["reason"] = "Vorschau und Erkennung sind auf die ersten20Seiten begrenzt. Weitere Seiten im Original prüfen."
		}
	} else {
		detail["reason"] = "Die tatsächliche Seitenzahl kann lokal derzeit nicht bestätigt werden. Keine Seiten oder OCR-Werte werden erfunden."
	}
	detail["pages"] = pages
	result["detail"] = detail
	return nil
}

func (s *Server) financeReceiptPageAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	page, err := strconv.Atoi(r.PathValue("page"))
	if err != nil || page < 1 || page > 20 || strconv.Itoa(page) != r.PathValue("page") || r.URL.RawQuery != "" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	case <-ctx.Done():
		s.fail(w, r, store.ErrConflict)
		return
	}
	source, err := s.Store.FinanceReceiptSource(ctx, identity(r), r.PathValue("attachment"))
	var raw, raster []byte
	if err == nil {
		raw, err = financeReceiptFile(s.Cfg.UploadDir, source)
	}
	if err == nil {
		count, countErr := financeReceiptKnownPages(ctx, source, raw)
		if countErr != nil || page > *count {
			err = store.Invalid{Message: "Diese tatsächlich vorhandene Belegseite kann nicht bestätigt werden"}
		}
	}
	if err == nil {
		raster, err = financeReceiptRaster(ctx, raw, source.MediaType, page)
	}
	if err == nil {
		latest, currentErr := s.Store.FinanceReceiptSource(ctx, identity(r), source.AttachmentID)
		if currentErr != nil {
			err = currentErr
		} else if latest != source {
			err = store.ErrConflict
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `inline; filename="belegseite.png"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(raster)))
	_, _ = w.Write(raster)
}

func financeReceiptInput(input store.FinanceReceiptRecognizeInput, source store.FinanceReceiptSource) error {
	exponent, ok := finance.CurrencyExponent(input.Currency)
	if !ok || input.Exponent != exponent || input.Generation != source.Generation || input.SourceVersion != source.SourceVersion || input.SourceHash != source.SHA256 ||
		len(input.Pages) < 1 || len(input.Pages) > 3 || !slices.Contains([]string{".", ","}, input.DecimalSeparator) ||
		!slices.Contains([]string{"", ".", ",", " "}, input.GroupSeparator) || input.GroupSeparator == input.DecimalSeparator {
		return store.Invalid{Message: "Aktuelle Quelle, Währung und eine bis drei Belegseiten ausdrücklich auswählen"}
	}
	seen := map[int]bool{}
	for _, page := range input.Pages {
		if page < 1 || page > 20 || seen[page] || source.MediaType != "application/pdf" && page != 1 {
			return store.Invalid{Message: "Jede tatsächlich vorhandene Belegseite höchstens einmal wählen"}
		}
		seen[page] = true
	}
	return nil
}

func (s *Server) financeReceiptRecognizeAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if !identity(r).CanWrite() {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	if !s.Cfg.OCREnabled || !value.ValidUUID(r.Header.Get("Idempotency-Key")) {
		s.fail(w, r, store.Invalid{Message: "Lokale OCR ist nicht eingerichtet oder der Vorgangsschlüssel fehlt"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	rawInput, err := financeJSON(w, r)
	var input store.FinanceReceiptRecognizeInput
	if err == nil {
		err = store.DecodeFinanceDocument(rawInput, &input)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	case <-ctx.Done():
		s.fail(w, r, store.ErrConflict)
		return
	}
	source, err := s.Store.FinanceReceiptSource(ctx, identity(r), r.PathValue("attachment"))
	if err == nil {
		if !source.CanReview {
			err = store.ErrForbidden
		}
	}
	if err == nil {
		err = financeReceiptInput(input, source)
	}
	var raw []byte
	if err == nil {
		raw, err = financeReceiptFile(s.Cfg.UploadDir, source)
	}
	result := store.FinanceReceiptSuggestions{RecognizedPages: []int{}, Lines: []store.FinanceReceiptLine{}, TotalCandidates: []store.FinanceReceiptTotalCandidate{}}
	if err == nil {
		result.PageCount, err = financeReceiptKnownPages(ctx, source, raw)
	}
	if err == nil {
		for _, page := range input.Pages {
			if page > *result.PageCount {
				err = store.Invalid{Message: "Gewählte Seite liegt außerhalb des tatsächlichen Originals"}
				break
			}
			raster, rasterErr := financeReceiptRaster(ctx, raw, source.MediaType, page)
			if rasterErr != nil {
				err = rasterErr
				break
			}
			tsv, ocrErr := financeReceiptLocal(ctx, raster, "/usr/bin/tesseract", "stdin", "stdout", "-l", "deu+eng", "--psm", "6", "tsv")
			if ocrErr != nil {
				err = store.Invalid{Message: "Lokale Erkennung konnte nicht vollständig ausgeführt werden. Manuelle Prüfung bleibt möglich"}
				break
			}
			lines, totals, parseErr := financeReceiptTSV(tsv, page, source.SHA256, input)
			if parseErr != nil || len(result.Lines)+len(lines) > 100 || len(result.TotalCandidates)+len(totals) > 20 {
				err = store.Invalid{Message: "Erkannte Angaben überschreiten die begrenzte Prüfansicht. Kleinere Seitenauswahl verwenden"}
				break
			}
			for index := range lines {
				lines[index].Position = len(result.Lines) + index
			}
			result.Lines = append(result.Lines, lines...)
			result.TotalCandidates = append(result.TotalCandidates, totals...)
			result.RecognizedPages = append(result.RecognizedPages, page)
		}
	}
	result.AmbiguousTotal = len(result.TotalCandidates) != 1
	// Executing all requested pages is not evidence that every item was read.
	// Literal, unparsed lines remain in the review; no completeness is invented.
	result.RecognitionComplete = false
	if err == nil {
		latest, currentErr := s.Store.FinanceReceiptSource(ctx, identity(r), source.AttachmentID)
		if currentErr != nil {
			err = currentErr
		} else if latest != source {
			err = store.ErrConflict
		}
	}
	var out store.Record
	if err == nil {
		out, err = s.Store.FinanceSaveReceiptSuggestions(ctx, identity(r), r.Header.Get("Idempotency-Key"), trace(r), source, input, result)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func financeReceiptAmount(raw string, exponent int, decimal, group string) *string {
	if len(raw) > 40 || !slices.Contains([]int{0, 2, 3}, exponent) || decimal != "." && decimal != "," ||
		!slices.Contains([]string{"", ".", ",", " "}, group) || group == decimal {
		return nil
	}
	parts := strings.Split(raw, decimal)
	if len(parts) > 2 {
		return nil
	}
	whole := parts[0]
	sign := ""
	if strings.HasPrefix(whole, "-") {
		sign, whole = "-", strings.TrimPrefix(whole, "-")
	} else {
		whole = strings.TrimPrefix(whole, "+")
	}
	if group != "" && strings.Contains(whole, group) {
		groups := strings.Split(whole, group)
		if len(groups[0]) < 1 || len(groups[0]) > 3 {
			return nil
		}
		for _, part := range groups[1:] {
			if len(part) != 3 {
				return nil
			}
		}
		whole = strings.Join(groups, "")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > exponent {
			return nil
		}
	}
	digits := whole + fraction
	if whole == "" || len(digits) > 15 {
		return nil
	}
	for _, char := range digits {
		if char < '0' || char > '9' {
			return nil
		}
	}
	digits = strings.TrimLeft(whole+fraction+strings.Repeat("0", exponent-len(fraction)), "0")
	if digits == "" {
		digits, sign = "0", ""
	}
	minor := sign + digits
	if _, err := finance.ParseMinor(minor); err != nil {
		return nil
	}
	return &minor
}

func financeReceiptText(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= limit {
		return raw
	}
	raw = raw[:limit]
	for !utf8.ValidString(raw) {
		raw = raw[:len(raw)-1]
	}
	return raw
}

func financeReceiptTSV(raw []byte, page int, sourceHash string, input store.FinanceReceiptRecognizeInput) ([]store.FinanceReceiptLine, []store.FinanceReceiptTotalCandidate, error) {
	if len(raw) > 512<<10 || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) || page < 1 || page > 20 {
		return nil, nil, errors.New("invalid OCR evidence")
	}
	lines := []store.FinanceReceiptLine{}
	totals := []store.FinanceReceiptTotalCandidate{}
	textLines, order := map[string][]string{}, []string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	if !scanner.Scan() || scanner.Text() != "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext" {
		return nil, nil, errors.New("unrecognized OCR format")
	}
	words := 0
	for scanner.Scan() {
		columns := strings.SplitN(scanner.Text(), "\t", 12)
		if len(columns) != 12 {
			return nil, nil, errors.New("invalid OCR row")
		}
		if columns[0] != "5" || strings.TrimSpace(columns[11]) == "" {
			continue
		}
		for _, column := range columns[1:6] {
			integer, err := strconv.Atoi(column)
			if err != nil || integer < 1 || integer > 100000 {
				return nil, nil, errors.New("invalid OCR line identity")
			}
		}
		words++
		if words > 10000 || len(columns[11]) > 2000 {
			return nil, nil, errors.New("OCR evidence limit")
		}
		key := strings.Join(columns[2:5], "/")
		if _, exists := textLines[key]; !exists {
			if len(order) == 100 {
				return nil, nil, errors.New("OCR line limit")
			}
			order = append(order, key)
		}
		textLines[key] = append(textLines[key], columns[11])
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, errors.New("OCR evidence limit")
	}
	amountPattern := regexp.MustCompile(`(?:^|\s)([-+]?[0-9][0-9., ]*)(?:\s*(?:[A-Z]{3}|€))?\s*$`)
	for index, key := range order {
		text := strings.TrimSpace(strings.Join(textLines[key], " "))
		if len(text) > 2000 {
			return nil, nil, errors.New("OCR line text limit")
		}
		lineNumber := index + 1
		label := financeReceiptText(text, 300)
		digest := sha256.Sum256(fmt.Appendf(nil, "receipt-line-v1/%s/%d/%s/%s", sourceHash, page, key, text))
		digest[6], digest[8] = digest[6]&0x0f|0x50, digest[8]&0x3f|0x80
		id := fmt.Sprintf("%x-%x-%x-%x-%x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
		line := store.FinanceReceiptLine{ID: id, Position: index, Label: label, SourceText: text, SourceKind: "ocr", PageNumber: page, SourceLine: lineNumber}
		match := amountPattern.FindStringSubmatch(text)
		if len(match) == 2 {
			amount := financeReceiptAmount(strings.TrimSpace(match[1]), input.Exponent, input.DecimalSeparator, input.GroupSeparator)
			lower := strings.ToLower(text)
			total := slices.ContainsFunc([]string{"gesamt", "summe", "total", "zu zahlen", "betrag"}, func(word string) bool { return strings.Contains(lower, word) })
			if total && amount != nil {
				if len(totals) == 20 {
					return nil, nil, errors.New("OCR total candidate limit")
				}
				totals = append(totals, store.FinanceReceiptTotalCandidate{Minor: *amount, PageNumber: page, SourceLine: lineNumber, SourceText: text})
			} else {
				line.AmountMinor = amount
			}
		}
		// Tesseract supplies word confidence, not a receipt-line confidence.
		// Preserve unknown instead of inventing an aggregate score or quantity.
		lines = append(lines, line)
	}
	return lines, totals, nil
}
