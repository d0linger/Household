package maintenance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/config"
)

func TestS3BucketProbeIsSignedAndReadOnly(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		code   string
	}{
		{"available", http.StatusOK, "ok"},
		{"access denied", http.StatusForbidden, "access_denied"},
		{"bucket missing", http.StatusNotFound, "bucket_missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			probes := make(chan struct{}, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || strings.TrimSuffix(r.URL.Path, "/") != "/s3-dp" || r.URL.RawQuery != "" ||
					!strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=fixture-access/") {
					t.Error("probe must make a signed HEAD request to the bucket")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				probes <- struct{}{}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()
			target := config.S3Connection{
				Endpoint: strings.TrimPrefix(server.URL, "http://"), Bucket: "s3-dp", Region: "us-east-1",
				AccessKey: "fixture-access", SecretKey: "fixture-secret-only",
			}
			if got := ResultCode(testS3Bucket(t.Context(), target)); got != tt.code {
				t.Fatalf("result %q, want %q", got, tt.code)
			}
			if len(probes) != 1 {
				t.Fatal("expected a single authenticated bucket probe")
			}
		})
	}
}
