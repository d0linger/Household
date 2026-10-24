package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

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
