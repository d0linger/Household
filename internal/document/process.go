package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type cappedOutput struct {
	b     bytes.Buffer
	limit int
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.b.Len() {
		return 0, errors.New("document tool output exceeds limit")
	}
	return w.b.Write(p)
}

func localTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "pdftotext" && name != "pdfinfo" && name != "pdftoppm" && name != "pdfseparate" && name != "pdfunite" && name != "tesseract" {
		return nil, errors.New("unsupported document tool")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	output := &cappedOutput{limit: 96000}
	diagnostic := &cappedOutput{limit: 4096}
	// security: name is the closed local executable allowlist; no shell or user-supplied option is used.
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 G702 -- closed executable allowlist and separate arguments
	cmd.Stdout, cmd.Stderr = output, diagnostic
	if err := cmd.Run(); err != nil {
		return nil, errors.New("local document tool unavailable, rejected input, or resource limit reached")
	}
	if !utf8.Valid(output.b.Bytes()) {
		return nil, errors.New("document tool returned invalid text encoding")
	}
	return output.b.Bytes(), nil
}

type Extracted struct {
	Text     string
	Provider string
	Pages    int
	Preview  []byte
}

// Extract reads existing PDF text before optional local OCR. Originals are never rewritten.
func Extract(ctx context.Context, root *os.Root, key, media string, ocr bool) (Extracted, error) {
	result := Extracted{Pages: 1}
	if !safeKey(key) {
		return result, errors.New("invalid original key")
	}
	temp, err := os.MkdirTemp(root.Name(), "document-work-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	source := filepath.Join(root.Name(), key)
	if media == "application/pdf" {
		info, err := localTool(ctx, "pdfinfo", source)
		if err != nil {
			return result, err
		}
		result.Pages = 0
		for _, line := range strings.Split(string(info), "\n") {
			if strings.HasPrefix(line, "Pages:") {
				result.Pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			}
		}
		if result.Pages < 1 || result.Pages > 100 {
			return result, errors.New("document PDF page limit exceeded")
		}
		text, err := localTool(ctx, "pdftotext", "-f", "1", "-l", strconv.Itoa(result.Pages), "-enc", "UTF-8", "-nopgbrk", source, "-")
		if err != nil {
			return result, err
		}
		result.Text, result.Provider = strings.TrimSpace(string(text)), "pdf_text"
		if len(result.Text) > 80000 {
			return result, errors.New("PDF text exceeds document limit")
		}
		// A bounded first-page preview is safe to render inline; the raw PDF remains download-only.
		_, previewErr := localTool(ctx, "pdftoppm", "-f", "1", "-l", "1", "-singlefile", "-scale-to", "1400", "-png", source, filepath.Join(temp, "preview"))
		if previewErr == nil {
			result.Preview, _ = readBounded(filepath.Join(temp, "preview.png"), 3<<20)
		}
		if result.Text != "" || !ocr {
			return result, nil
		}
		if result.Pages > 20 {
			return result, errors.New("OCR supports at most twenty pages per reviewed processing request")
		}
		var textParts []string
		for page := 1; page <= result.Pages; page++ {
			prefix := filepath.Join(temp, "ocr")
			if _, err = localTool(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-scale-to", "2400", "-png", source, prefix); err != nil {
				return result, err
			}
			text, err = localTool(ctx, "tesseract", prefix+".png", "stdout", "-l", "deu+eng")
			if err != nil {
				return result, err
			}
			textParts = append(textParts, string(text))
			if len(strings.Join(textParts, "\n")) > 80000 {
				return result, errors.New("OCR text exceeds document limit")
			}
		}
		result.Text, result.Provider = strings.TrimSpace(strings.Join(textParts, "\n")), "tesseract"
		return result, nil
	}
	if media != "image/jpeg" && media != "image/png" {
		return result, errors.New("unsupported document media")
	}
	if !ocr {
		result.Provider = "manual"
		return result, nil
	}
	text, err := localTool(ctx, "tesseract", source, "stdout", "-l", "deu+eng")
	if err != nil {
		return result, err
	}
	result.Text, result.Provider = strings.TrimSpace(string(text)), "tesseract"
	if len(result.Text) > 80000 {
		return result, errors.New("OCR text exceeds document limit")
	}
	return result, nil
}

func safeKey(key string) bool {
	if len(key) != 36 {
		return false
	}
	for i, c := range key {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func readBounded(path string, limit int64) ([]byte, error) {
	// security: paths are generated inside the operator-owned temporary work directory.
	f, err := os.Open(path) // #nosec G304 G703 -- generated local worker path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("document result file exceeds limit")
	}
	return raw, nil
}

type ScanSource struct {
	Key   string
	Media string
	Page  int
}

func Assemble(ctx context.Context, root *os.Root, pages []ScanSource) ([]byte, error) {
	if len(pages) < 1 || len(pages) > 20 {
		return nil, errors.New("derived PDF supports one to twenty selected pages")
	}
	temp, err := os.MkdirTemp(root.Name(), "document-work-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	workRoot, err := os.OpenRoot(temp)
	if err != nil {
		return nil, err
	}
	defer func() { _ = workRoot.Close() }()
	files := []string{}
	for i, page := range pages {
		if !safeKey(page.Key) || page.Page < 1 || page.Page > 100 {
			return nil, errors.New("invalid scan source")
		}
		target := filepath.Join(temp, fmt.Sprintf("page-%03d.pdf", i+1))
		source := filepath.Join(root.Name(), page.Key)
		if page.Media == "application/pdf" {
			pattern := filepath.Join(temp, fmt.Sprintf("source-%03d-%%d.pdf", i+1))
			if _, err = localTool(ctx, "pdfseparate", "-f", strconv.Itoa(page.Page), "-l", strconv.Itoa(page.Page), source, pattern); err != nil {
				return nil, err
			}
			target = filepath.Join(temp, fmt.Sprintf("source-%03d-%d.pdf", i+1, page.Page))
		} else {
			if page.Page != 1 || page.Media != "image/png" && page.Media != "image/jpeg" {
				return nil, errors.New("invalid image page")
			}
			raw, err := root.ReadFile(page.Key)
			if err != nil || len(raw) > 10<<20 {
				return nil, errors.New("scan source unavailable")
			}
			pdf, err := imagePDF(raw)
			if err != nil {
				return nil, err
			}
			if err = workRoot.WriteFile(fmt.Sprintf("page-%03d.pdf", i+1), pdf, 0600); err != nil {
				return nil, err
			}
		}
		files = append(files, target)
	}
	output := filepath.Join(temp, "combined.pdf")
	if _, err = localTool(ctx, "pdfunite", append(files, output)...); err != nil {
		return nil, err
	}
	return readBounded(output, 10<<20)
}

func imagePDF(raw []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		return nil, errors.New("invalid bounded scan image")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
	var jpegData bytes.Buffer
	if err = jpeg.Encode(&jpegData, flat, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	width, height := cfg.Width, cfg.Height
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	object := func(n int, body []byte) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n", n)
		out.Write(body)
		out.WriteString("\nendobj\n")
	}
	object(1, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	object(2, []byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"))
	object(3, fmt.Appendf(nil, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>", width, height))
	imageBody := fmt.Appendf(nil, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, jpegData.Len())
	imageBody = append(imageBody, jpegData.Bytes()...)
	imageBody = append(imageBody, []byte("\nendstream")...)
	object(4, imageBody)
	commands := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", width, height)
	object(5, []byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(commands), commands)))
	xref := out.Len()
	out.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	if out.Len() > 10<<20 {
		return nil, errors.New("derived image PDF exceeds limit")
	}
	return out.Bytes(), nil
}
