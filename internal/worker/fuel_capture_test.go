package worker

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"household/internal/config"
	"household/internal/store"
)

func TestFuelOCRBufferEnforcesByteLimitWithoutPartialOverflow(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		limit                    int
		initial, overflow, final string
	}{
		{"exact limit", 4, "abcd", "e", ""},
		{"overflow does not poison remaining capacity", 4, "a", "bcde", "bcd"},
		{"UTF8 counts bytes", 3, "ä", "ö", "x"},
		{"empty buffer overflow", 4, "", "abcde", "abcd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buffer := fuelOCRBuffer{limit: tc.limit}
			if n, err := buffer.Write([]byte(tc.initial)); err != nil || n != len(tc.initial) {
				t.Fatal("initial bounded write", n, err)
			}
			if n, err := buffer.Write([]byte(tc.overflow)); err == nil || n != 0 || buffer.String() != tc.initial {
				t.Fatal("overflow partially wrote or changed prior output", n, buffer.String(), err)
			}
			if n, err := buffer.Write([]byte(tc.final)); err != nil || n != len(tc.final) || buffer.String() != tc.initial+tc.final || buffer.Len() != tc.limit {
				t.Fatal("remaining bounded capacity changed", n, buffer.String(), err)
			}
		})
	}
}

func TestFuelOCRRejectsInvalidSourceBeforeExternalTools(t *testing.T) {
	const key = "f0000000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		name, key string
		size      int64
	}{
		{"non UUID key", "not-a-storage-key", 1},
		{"parent traversal", "../secret", 1},
		{"absolute path", "C:/private/document.png", 1},
		{"zero size", key, 0},
		{"negative size", key, -1},
		{"over input bound", key, (10 << 20) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A missing root ensures no external executable or file is needed to
			// establish these argument failures. Assert their actual early error.
			w := Worker{Cfg: config.Config{UploadDir: filepath.Join(t.TempDir(), "absent")}}
			text, err := w.fuelOCR(t.Context(), store.FuelCaptureSource{StorageKey: tc.key, SizeBytes: tc.size, MediaType: "image/png"})
			if err == nil || err.Error() != "invalid Fuel capture file" || text != "" {
				t.Fatal("invalid source reached filesystem/external fallback", text, err)
			}
		})
	}
}

func TestFuelOCRChecksImmutableLocalFileAndDecodedPixelBounds(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// Change only the tiny PNG's IHDR dimensions and its CRC. DecodeConfig can
	// read the legitimate header without allocating a twenty-million-pixel image.
	wide := bytes.Clone(picture.Bytes())
	binary.BigEndian.PutUint32(wide[16:20], 20_000_001)
	binary.BigEndian.PutUint32(wide[20:24], 1)
	binary.BigEndian.PutUint32(wide[29:33], crc32.ChecksumIEEE(wide[12:29]))
	decoded, err := png.DecodeConfig(bytes.NewReader(wide))
	if err != nil || decoded.Width != 20_000_001 || decoded.Height != 1 {
		t.Fatal("oversized PNG fixture must contain a valid decodable header", decoded, err)
	}
	for _, tc := range []struct {
		name, media, want               string
		input                           []byte
		wrongSize, wrongHash, directory bool
	}{
		{name: "size changed", media: "image/png", input: picture.Bytes(), wrongSize: true, want: "fuel capture file size changed"},
		{name: "digest changed", media: "image/png", input: picture.Bytes(), wrongHash: true, want: "fuel capture digest changed"},
		{name: "regular file required", media: "image/png", directory: true, want: "fuel capture file size changed"},
		{name: "PDF magic", media: "application/pdf", input: []byte("not a PDF"), want: "invalid Fuel capture PDF"},
		{name: "unsupported media", media: "text/plain", input: []byte("plain text"), want: "unsupported Fuel capture media"},
		{name: "malformed image", media: "image/png", input: []byte("not a PNG"), want: "fuel capture image limit"},
		{name: "pixel bound", media: "image/png", input: wide, want: "fuel capture image limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			const key = "f0000000-0000-4000-8000-000000000001"
			if tc.directory {
				if err := os.Mkdir(filepath.Join(root, key), 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(root, key), tc.input, 0600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(tc.input)
			source := store.FuelCaptureSource{StorageKey: key, MediaType: tc.media, SizeBytes: int64(len(tc.input)), SHA256: hex.EncodeToString(digest[:])}
			if tc.wrongSize {
				source.SizeBytes++
			}
			if tc.wrongHash {
				source.SHA256 = strings.Repeat("00", 32)
			}
			if tc.directory {
				source.SizeBytes = 1
			}
			w := Worker{Cfg: config.Config{UploadDir: root}}
			text, err := w.fuelOCR(t.Context(), source)
			if err == nil || err.Error() != tc.want || text != "" {
				t.Fatalf("pre-external rejection = %q, %v; want %s", text, err, tc.want)
			}
		})
	}
}
