// Package documentinput implements bounded optional read-only document inputs.
package documentinput

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	WatchDir       string
	IMAPAddress    string
	IMAPUser       string
	IMAPPassword   string
	IMAPMailbox    string
	PaperlessURL   string
	PaperlessToken string
	PaperlessTag   int
	RootCAs        *x509.CertPool
}

func (c Config) Validate() error {
	if c.WatchDir != "" && !filepath.IsAbs(c.WatchDir) {
		return errors.New("document watch folder must be absolute")
	}
	if c.IMAPAddress != "" {
		host, port, err := net.SplitHostPort(c.IMAPAddress)
		p, e := strconv.Atoi(port)
		if err != nil || host == "" || e != nil || p < 1 || p > 65535 || c.IMAPUser == "" || c.IMAPPassword == "" || c.IMAPMailbox == "" {
			return errors.New("complete document IMAP TLS configuration required")
		}
	}
	if c.PaperlessURL != "" {
		u, err := url.Parse(c.PaperlessURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || c.PaperlessToken == "" || c.PaperlessTag < 1 {
			return errors.New("document Paperless requires an HTTPS origin, token and explicit tag scope")
		}
	}
	for _, v := range []string{c.IMAPUser, c.IMAPPassword, c.IMAPMailbox, c.PaperlessToken} {
		if len(v) > 4096 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid document input credential or mailbox")
		}
	}
	return nil
}

func (c Config) TLS(host string) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: c.RootCAs}
}
func (c Config) Available(adapter string) bool {
	switch adapter {
	case "watch":
		return c.WatchDir != ""
	case "imap":
		return c.IMAPAddress != ""
	case "paperless":
		return c.PaperlessURL != ""
	}
	return false
}

type Candidate struct {
	Key   string
	Name  string
	Bytes []byte
}
type Cursor struct {
	UIDValidity uint64 `json:"uid_validity"`
	LastUID     uint64 `json:"last_uid"`
	Page        int    `json:"page"`
	After       string `json:"after"`
}
