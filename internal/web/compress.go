package web

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// gzipCache holds compressed copies of embedded text assets, built on first use.
var gzipCache sync.Map // name -> []byte (nil when compression does not pay off)

// compressedAsset returns a gzip copy of a compressible asset, or nil.
func compressedAsset(name string, data []byte) []byte {
	if !strings.HasSuffix(name, ".css") && !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".svg") || len(data) < 1024 {
		return nil
	}
	if cached, ok := gzipCache.Load(name); ok {
		compressed, _ := cached.([]byte)
		return compressed
	}
	var buf bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := writer.Write(data); err != nil || writer.Close() != nil || buf.Len() >= len(data) {
		gzipCache.Store(name, []byte(nil))
		return nil
	}
	compressed := buf.Bytes()
	gzipCache.Store(name, compressed)
	return compressed
}

// acceptsGzip reports whether the request accepts gzip with a nonzero quality.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			params = strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return params != "q=0" && params != "q=0.0" && params != "q=0.00" && params != "q=0.000"
		}
	}
	return false
}
