package recipeimport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// FetchResult contains only bounded page bytes and a query-free public URL.
// It is untrusted input for a subsequent inert preview, never a recipe write.
type FetchResult struct {
	Body         []byte
	CanonicalURL string
	ContentType  string
	SHA256       string
}

type Fetcher struct {
	slots  chan struct{}
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
	roots  *x509.CertPool
}

func NewFetcher() *Fetcher {
	return &Fetcher{slots: make(chan struct{}, 2), lookup: net.DefaultResolver.LookupNetIP,
		dial: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
}

var recipeExcludedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicRecipeIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Zone() != "" {
		return false
	}
	for _, prefix := range recipeExcludedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func fetchURL(raw string) (*url.URL, string, error) {
	if len(raw) == 0 || len(raw) > 2048 {
		return nil, "", errors.New("URL fehlt oder ist zu lang")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" ||
		u.Scheme != "https" || u.Host == "" || u.Port() != "" && u.Port() != "443" {
		return nil, "", errors.New("nur HTTPS:443 ohne Zugangsdaten und Fragment ist zulässig")
	}
	canonical, err := CanonicalURL(raw)
	if err != nil {
		return nil, "", err
	}
	u.Host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return u, canonical, nil
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (FetchResult, error) {
	var result FetchResult
	if f == nil || f.lookup == nil || f.dial == nil || f.slots == nil {
		return result, errors.New("URL-Abruf ist nicht verfügbar")
	}
	u, canonical, err := fetchURL(rawURL)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	select {
	case f.slots <- struct{}{}:
		defer func() { <-f.slots }()
	case <-ctx.Done():
		return result, errors.New("URL-Abruf dauerte zu lange")
	}
	host := u.Hostname()
	addresses, err := f.lookup(ctx, "ip", host)
	if err != nil || len(addresses) == 0 || len(addresses) > 16 {
		return result, errors.New("öffentlicher DNS-Eintrag ist nicht verfügbar")
	}
	for _, ip := range addresses {
		if !publicRecipeIP(ip) {
			return result, errors.New("URL-Ziel ist nicht öffentlich")
		}
	}
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, DisableKeepAlives: true,
		MaxConnsPerHost: 1, TLSHandshakeTimeout: 2 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: f.roots},
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			actualHost, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil || network != "tcp" || actualHost != host || port != "443" {
				return nil, errors.New("URL-Dialziel wurde geändert")
			}
			for _, ip := range addresses {
				connection, dialErr := f.dial(dialCtx, "tcp", net.JoinHostPort(ip.String(), "443"))
				if dialErr == nil {
					return connection, nil
				}
				if dialCtx.Err() != nil {
					return nil, dialCtx.Err()
				}
			}
			return nil, errors.New("öffentliche Verbindung nicht erreichbar")
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return result, errors.New("URL-Anfrage ist ungültig")
	}
	request.Header.Set("Accept", "text/html, application/ld+json, application/json")
	request.Header.Set("Accept-Encoding", "identity, gzip")
	request.Header.Set("User-Agent", "Household-RecipeImport/1")
	response, err := client.Do(request)
	if err != nil {
		return result, errors.New("URL-Abruf fehlgeschlagen oder abgelaufen")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.ContentLength > MaxInputBytes {
		return result, errors.New("URL antwortete nicht mit begrenztem Rezeptinhalt")
	}
	media := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if media != "text/html" && media != "application/ld+json" && media != "application/json" {
		return result, errors.New("URL-Inhaltstyp wird nicht unterstützt")
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, MaxInputBytes+1))
	if err != nil || len(encoded) > MaxInputBytes {
		return result, errors.New("URL-Antwort ist beschädigt oder zu groß")
	}
	var body []byte
	switch strings.ToLower(response.Header.Get("Content-Encoding")) {
	case "", "identity":
		body = encoded
	case "gzip":
		reader, openErr := gzip.NewReader(bytes.NewReader(encoded))
		if openErr != nil {
			return result, errors.New("URL-Kompression ist ungültig")
		}
		body, err = io.ReadAll(io.LimitReader(reader, MaxExpandedBytes+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || len(body) > MaxExpandedBytes || len(body) > len(encoded)*20 {
			return result, errors.New("URL-Antwort ist entpackt zu groß")
		}
	default:
		return result, errors.New("URL-Kompression wird nicht unterstützt")
	}
	if len(body) == 0 {
		return result, errors.New("URL enthält kein Rezept")
	}
	sum := sha256.Sum256(body)
	return FetchResult{Body: body, CanonicalURL: canonical, ContentType: media, SHA256: hex.EncodeToString(sum[:])}, nil
}
