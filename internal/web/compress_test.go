package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"gzip": true, "br, gzip;q=0.8": true, "GZIP": true, "gzip;q=0": false, "gzip; q=0.0": false, "br": false, "": false, "identity": false,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", header)
		if got := acceptsGzip(r); got != want {
			t.Fatalf("%q: %v", header, got)
		}
	}
}

func TestCompressedAssetRoundTripsAndSkipsSmallOrBinary(t *testing.T) {
	data := []byte(strings.Repeat("body{color:red}\n", 200))
	compressed := compressedAsset("compress-test.css", data)
	if compressed == nil || len(compressed) >= len(data) {
		t.Fatal("text asset not compressed")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(plain, data) {
		t.Fatal("round trip differs")
	}
	if compressedAsset("compress-test.png", data) != nil || compressedAsset("compress-small.js", []byte("x")) != nil {
		t.Fatal("binary or tiny asset compressed")
	}
}
