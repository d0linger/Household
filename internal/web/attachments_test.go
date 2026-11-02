package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOriginalName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, want string
	}{
		{"short", "Beleg März.pdf", "Beleg März.pdf"},
		{"long multibyte keeps extension", "abc" + strings.Repeat("ö", 200) + ".png", "abc" + strings.Repeat("ö", 193) + ".png"},
		{"long without extension cuts on a character", strings.Repeat("ä", 250), strings.Repeat("ä", 200)},
		{"overlong extension is dropped", strings.Repeat("a", 195) + "." + strings.Repeat("x", 20), strings.Repeat("a", 195) + "." + strings.Repeat("x", 4)},
		{"invalid bytes are replaced", "scan\xff\xfe.jpg", "scan��.jpg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := originalName(tc.input)
			if got != tc.want || !utf8.ValidString(got) || utf8.RuneCountInString(got) > 200 {
				t.Fatalf("originalName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeUpload(t *testing.T) {
	t.Parallel()
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	raw := append(data.Bytes(), []byte("private metadata trailer")...)
	clean, kind, err := sanitizeUpload(raw)
	if err != nil || kind != "image/png" || bytes.Contains(clean, []byte("private metadata trailer")) {
		t.Fatalf("image sanitation failed: %v", err)
	}
	if _, _, err = sanitizeUpload([]byte(`<svg onload="alert(1)"/>`)); err == nil {
		t.Fatal("active SVG accepted")
	}
}
