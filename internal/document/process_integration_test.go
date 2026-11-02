//go:build integration

package document

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

func TestDocumentsProcessActualPDFTextPreviewAndOrderedSplitMerge(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	key := "11111111-1111-4111-8111-111111111111"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	content := "BT /F1 16 Tf 20 150 Td (Household invoice reference 2026) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, body := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := pdf.Len()
	pdf.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	if err = root.WriteFile(key, pdf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := Extract(ctx, root, key, "application/pdf", false)
	if err != nil || result.Provider != "pdf_text" || result.Pages != 1 || !bytes.Contains([]byte(result.Text), []byte("Household invoice reference 2026")) || len(result.Preview) == 0 {
		t.Fatal(result, err)
	}
	ocrKey := "55555555-5555-4555-8555-555555555555"
	if err = root.WriteFile(ocrKey, result.Preview, 0600); err != nil {
		t.Fatal(err)
	}
	ocr, err := Extract(ctx, root, ocrKey, "image/png", true)
	if err != nil || ocr.Provider != "tesseract" || !bytes.Contains([]byte(ocr.Text), []byte("Household")) {
		t.Fatal("actual local raster OCR", ocr, err)
	}
	imageKey := "22222222-2222-4222-8222-222222222222"
	var photo bytes.Buffer
	if err = png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 25, 20))); err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile(imageKey, photo.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	combined, err := Assemble(ctx, root, []ScanSource{{Key: imageKey, Media: "image/png", Page: 1}, {Key: key, Media: "application/pdf", Page: 1}})
	if err != nil {
		t.Fatal(err)
	}
	mergedKey := "33333333-3333-4333-8333-333333333333"
	if err = root.WriteFile(mergedKey, combined, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = Extract(ctx, root, mergedKey, "application/pdf", false)
	if err != nil || result.Pages != 2 {
		t.Fatal("actual merged pages", result, err)
	}
	split, err := Assemble(ctx, root, []ScanSource{{Key: mergedKey, Media: "application/pdf", Page: 2}})
	if err != nil {
		t.Fatal(err)
	}
	splitKey := "44444444-4444-4444-8444-444444444444"
	if err = root.WriteFile(splitKey, split, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = Extract(ctx, root, splitKey, "application/pdf", false)
	if err != nil || result.Pages != 1 || !bytes.Contains([]byte(result.Text), []byte("Household invoice")) {
		t.Fatal("actual split text", result, err)
	}
	original, err := root.ReadFile(key)
	if err != nil || !bytes.Equal(original, pdf.Bytes()) {
		t.Fatal("original was modified", err)
	}
}
