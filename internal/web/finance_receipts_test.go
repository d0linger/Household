package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"household/internal/store"
)

func TestFinanceReceiptOutputBudget(t *testing.T) {
	buffer := &financeReceiptBuffer{limit: 4}
	if n, err := buffer.Write([]byte("four")); n != 4 || err != nil {
		t.Fatal("exact output boundary refused")
	}
	if n, err := buffer.Write([]byte("x")); n != 0 || err == nil || buffer.String() != "four" {
		t.Fatal("output overflow was partially retained")
	}
}

func financeReceiptTestImage(t *testing.T) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	picture.Set(0, 0, color.NRGBA{R: 128, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestFinanceReceiptVerifiedLocalSource(t *testing.T) {
	raw, directory := financeReceiptTestImage(t), t.TempDir()
	digest := sha256.Sum256(raw)
	source := store.FinanceReceiptSource{StorageKey: "00000000-0000-4000-8000-000000000001", MediaType: "image/png", SizeBytes: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])}
	if err := os.WriteFile(filepath.Join(directory, source.StorageKey), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		edit  func(*store.FinanceReceiptSource)
		valid bool
	}{
		{"exact_regular_bytes", func(*store.FinanceReceiptSource) {}, true},
		{"changed_hash", func(s *store.FinanceReceiptSource) { s.SHA256 = strings.Repeat("0", 64) }, false},
		{"wrong_length", func(s *store.FinanceReceiptSource) { s.SizeBytes++ }, false},
		{"traversal_key", func(s *store.FinanceReceiptSource) { s.StorageKey = "../outside" }, false},
		{"oversized", func(s *store.FinanceReceiptSource) { s.SizeBytes = financeReceiptMaxBytes + 1 }, false},
		{"mismatched_declared_media", func(s *store.FinanceReceiptSource) { s.MediaType = "image/jpeg" }, false},
		{"unapproved_media", func(s *store.FinanceReceiptSource) { s.MediaType = "image/svg+xml" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := source
			test.edit(&candidate)
			got, err := financeReceiptFile(directory, candidate)
			if (err == nil) != test.valid || test.valid && !bytes.Equal(got, raw) {
				t.Fatal("source identity/byte validation did not match declared boundary")
			}
		})
	}
}

func TestFinanceReceiptRasterAndPixelBoundary(t *testing.T) {
	raw := financeReceiptTestImage(t)
	raster, err := financeReceiptRaster(t.Context(), raw, "image/png", 1)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raster))
	if err != nil || format != "png" || config.Width != 2 || config.Height != 3 {
		t.Fatal("actual raster dimensions or MIME changed")
	}
	if _, err := financeReceiptRaster(t.Context(), raw, "image/png", 2); err == nil {
		t.Fatal("image fabricated a second page")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := financeReceiptRaster(cancelled, raw, "image/png", 1); err == nil {
		t.Fatal("cancelled raster work was accepted")
	}
	oversized := bytes.Clone(raw)
	binary.BigEndian.PutUint32(oversized[16:20], 20001)
	binary.BigEndian.PutUint32(oversized[20:24], 20001)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	if _, err := financeReceiptImage(oversized, "image/png"); err == nil {
		t.Fatal("pixel boundary was checked after decode/allocation")
	}
}

func TestFinanceReceiptActualPageCount(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		want      int
	}{
		{"two_pages", "Title: synthetic\nPages:          2\nPage size: A4\n", 2},
		{"missing", "Title: no actual page count\n", 0},
		{"zero", "Pages: 0\n", 0},
		{"negative", "Pages: -1\n", 0},
		{"injected_metadata_line", "Title: text\nPages: 999\nPages: 2\n", 0},
		{"trailing_text", "Pages: 2 extra\n", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := financeReceiptPageCount([]byte(test.raw))
			if test.want == 0 {
				if err == nil {
					t.Fatal("unconfirmed count accepted")
				}
			} else if err != nil || got == nil || *got != test.want {
				t.Fatal("actual count lost")
			}
		})
	}
}

func TestFinanceReceiptRecognitionScope(t *testing.T) {
	source := store.FinanceReceiptSource{Generation: 3, SourceVersion: 2, SHA256: strings.Repeat("a", 64), MediaType: "application/pdf"}
	input := store.FinanceReceiptRecognizeInput{Generation: 3, SourceVersion: 2, SourceHash: source.SHA256, Currency: "EUR", Exponent: 2, Pages: []int{1, 2, 20}, DecimalSeparator: ","}
	for _, test := range []struct {
		name  string
		edit  func(*store.FinanceReceiptRecognizeInput)
		valid bool
	}{
		{"three_explicit_pages", func(*store.FinanceReceiptRecognizeInput) {}, true},
		{"duplicate", func(i *store.FinanceReceiptRecognizeInput) { i.Pages = []int{1, 1} }, false},
		{"too_many", func(i *store.FinanceReceiptRecognizeInput) { i.Pages = []int{1, 2, 3, 4} }, false},
		{"page_21", func(i *store.FinanceReceiptRecognizeInput) { i.Pages = []int{21} }, false},
		{"old_generation", func(i *store.FinanceReceiptRecognizeInput) { i.Generation-- }, false},
		{"old_source", func(i *store.FinanceReceiptRecognizeInput) { i.SourceVersion-- }, false},
		{"other_hash", func(i *store.FinanceReceiptRecognizeInput) { i.SourceHash = strings.Repeat("b", 64) }, false},
		{"wrong_exponent", func(i *store.FinanceReceiptRecognizeInput) { i.Exponent = 3 }, false},
		{"same_separators", func(i *store.FinanceReceiptRecognizeInput) { i.GroupSeparator = "," }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := input
			test.edit(&candidate)
			if (financeReceiptInput(candidate, source) == nil) != test.valid {
				t.Fatal("recognition scope mismatch")
			}
		})
	}
}

func TestFinanceReceiptExactRecognizedAmounts(t *testing.T) {
	for _, test := range []struct {
		name, raw            string
		exponent             int
		decimal, group, want string
	}{
		{"comma", "12,34", 2, ",", "", "1234"},
		{"negative_discount", "-1,00", 2, ",", "", "-100"},
		{"explicit_groups", "1.234,56", 2, ",", ".", "123456"},
		{"invalid_grouping", "1.2.3,45", 2, ",", ".", ""},
		{"ungiven_grouping", "1.234,56", 2, ",", "", ""},
		{"zero", "0,00", 2, ",", "", "0"},
		{"yen", "123", 0, ".", "", "123"},
		{"three_decimal", "0.001", 3, ".", "", "1"},
		{"unknown_precision", "1,234", 2, ",", "", ""},
		{"no_text_coercion", "1e3", 2, ",", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := financeReceiptAmount(test.raw, test.exponent, test.decimal, test.group)
			if test.want == "" {
				if got != nil {
					t.Fatal("ambiguous amount was invented")
				}
			} else if got == nil || *got != test.want {
				t.Fatal("exact amount changed")
			}
		})
	}
}

func TestFinanceReceiptLiteralTSVEvidence(t *testing.T) {
	header := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
	raw := header + "5\t1\t1\t1\t1\t1\t0\t0\t10\t10\t90\tBread\n" +
		"5\t1\t1\t1\t1\t2\t0\t0\t10\t10\t91\t8,00\n" +
		"5\t1\t1\t1\t2\t1\t0\t0\t10\t10\t-1\tDiscount -1,00\n" +
		"5\t1\t1\t1\t3\t1\t0\t0\t10\t10\t80\tTOTAL 7,00 EUR\n" +
		"5\t1\t1\t1\t4\t1\t0\t0\t10\t10\t-1\tUnparsed original text\n"
	input := store.FinanceReceiptRecognizeInput{Currency: "EUR", Exponent: 2, DecimalSeparator: ","}
	lines, totals, err := financeReceiptTSV([]byte(raw), 2, strings.Repeat("a", 64), input)
	if err != nil || len(lines) != 4 || len(totals) != 1 || totals[0].Minor != "700" || totals[0].PageNumber != 2 || totals[0].SourceLine != 3 {
		t.Fatal("literal source/page/total evidence lost")
	}
	if lines[0].AmountMinor == nil || *lines[0].AmountMinor != "800" || lines[1].AmountMinor == nil || *lines[1].AmountMinor != "-100" || lines[2].AmountMinor != nil || lines[3].AmountMinor != nil || lines[3].SourceText != "Unparsed original text" {
		t.Fatal("unparsed/total text was fabricated into an item amount")
	}
	for _, line := range lines {
		if line.Confidence != nil || line.Quantity != nil || line.UnitPrice != nil || line.Reviewed || line.SourceKind != "ocr" || line.PageNumber != 2 {
			t.Fatal("unreviewed OCR invented an aggregate confidence or fact")
		}
	}
	again, _, err := financeReceiptTSV([]byte(raw), 2, strings.Repeat("a", 64), input)
	if err != nil || again[0].ID != lines[0].ID {
		t.Fatal("same literal evidence did not retain stable proposal identity")
	}
	if _, _, err := financeReceiptTSV([]byte(header+strings.Repeat("x", 512<<10)), 1, "", input); err == nil {
		t.Fatal("oversized TSV accepted")
	}
	if _, _, err := financeReceiptTSV([]byte("untyped text"), 1, "", input); err == nil {
		t.Fatal("unstructured OCR treated as typed evidence")
	}
}
