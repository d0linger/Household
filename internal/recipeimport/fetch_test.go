package recipeimport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicRecipeAddressPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		address string
		allowed bool
	}{
		{"93.184.216.34", true}, {"2606:4700:4700::1111", true},
		{"127.0.0.1", false}, {"10.1.2.3", false}, {"169.254.169.254", false},
		{"100.64.0.1", false}, {"192.0.2.1", false}, {"2001:db8::1", false},
		{"64:ff9b::a9fe:a9fe", false}, {"::ffff:127.0.0.1", false},
	} {
		ip := netip.MustParseAddr(tc.address)
		if got := publicRecipeIP(ip); got != tc.allowed {
			t.Errorf("publicRecipeIP(%s)=%t, want %t", ip, got, tc.allowed)
		}
	}
	for _, raw := range []string{"http://recipes.example.com", "https://user:pass@recipes.example.com",
		"https://recipes.example.com:8443", "https://127.0.0.1/", "https://recipes.example.com/#fragment"} {
		if _, _, err := fetchURL(raw); err == nil {
			t.Errorf("accepted unsafe fetch URL %q", raw)
		}
	}
}

// A public DNS answer is intentionally mapped to a local TLS fixture. The
// production transport still verifies the original host and dials only a
// previously checked IP:443. No real network host is contacted by this test.
func TestFetcherPinnedTLSAndBoundedResponse(t *testing.T) {
	const host = "recipes.example.com"
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	compressedBomb := gzipBytes(t, []byte(strings.Repeat("a", 10000)))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != host || r.URL.Query().Get("secret") != "transient" {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/recipe":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<script type="application/ld+json">{"@type":"Recipe","name":"Soup"}</script>`))
		case "/redirect":
			w.Header().Set("Location", "https://169.254.169.254/latest/meta-data")
			w.WriteHeader(http.StatusFound)
		case "/too-large":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(strings.Repeat("a", MaxInputBytes+1)))
		case "/gzip-bomb":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(compressedBomb)
		case "/wrong-mime":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("not a recipe"))
		default:
			http.NotFound(w, r)
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{certificateDER},
		PrivateKey: key, Leaf: parsed}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	fetcher := NewFetcher()
	// A deployment's proxy environment must not redirect a reviewed target.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	fetcher.roots = roots
	fetcher.lookup = func(_ context.Context, network, name string) ([]netip.Addr, error) {
		if network != "ip" || name != host {
			t.Errorf("unexpected DNS question: %q %q", network, name)
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	fetcher.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "93.184.216.34:443" {
			t.Errorf("unexpected dial: %q %q", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	result, err := fetcher.Fetch(context.Background(), "https://"+host+"/recipe?secret=transient")
	if err != nil {
		connection, dialErr := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
		if dialErr != nil {
			t.Fatalf("fixture dial failed: %v; fetch: %v", dialErr, err)
		}
		client := tls.Client(connection, &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12})
		handshakeErr := client.Handshake()
		_ = client.Close()
		t.Fatalf("fixture TLS handshake: %v; fetch: %v", handshakeErr, err)
	}
	if err != nil || result.CanonicalURL != "https://"+host+"/recipe" ||
		!strings.Contains(string(result.Body), `"name":"Soup"`) || len(result.SHA256) != 64 {
		t.Fatalf("pinned fetch = %+v, %v", result, err)
	}
	if _, err := fetcher.Fetch(context.Background(), "https://"+host+"/redirect?secret=transient"); err == nil {
		t.Fatal("followed or accepted redirect")
	}
	if _, err := fetcher.Fetch(context.Background(), "https://"+host+"/too-large?secret=transient"); err == nil {
		t.Fatal("accepted overlong response")
	}
	if _, err := fetcher.Fetch(context.Background(), "https://"+host+"/gzip-bomb?secret=transient"); err == nil {
		t.Fatal("accepted excessive compression ratio")
	}
	if _, err := fetcher.Fetch(context.Background(), "https://"+host+"/wrong-mime?secret=transient"); err == nil {
		t.Fatal("accepted unexpected content type")
	}
	before := requests.Load()
	fetcher.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	if _, err := fetcher.Fetch(context.Background(), "https://different.example.com/recipe?secret=transient"); err == nil || requests.Load() != before {
		t.Fatal("TLS accepted a certificate for another host")
	}
	fetcher.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, err := fetcher.Fetch(context.Background(), "https://"+host+"/recipe?secret=transient"); err == nil || requests.Load() != before {
		t.Fatal("mixed public/private DNS answer reached transport")
	}
	fetcher.lookup = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	timed, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := fetcher.Fetch(timed, "https://"+host+"/recipe?secret=transient"); err == nil ||
		timed.Err() != context.DeadlineExceeded || requests.Load() != before {
		t.Fatal("expired URL fetch reached transport or returned success")
	}
}
