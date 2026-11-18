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
	"time"
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
	// Rejected names why the adapter could not turn this source item into a
	// document (for example malformed MIME or a message over the batch caps on
	// its own). Rejected items carry no bytes; the cursor still moves past them
	// so one permanently invalid item cannot halt the source.
	Rejected string
}
type Cursor struct {
	UIDValidity uint64 `json:"uid_validity"`
	LastUID     uint64 `json:"last_uid"`
	Page        int    `json:"page"`
	After       string `json:"after"`
	// Settling names the watch-inbox file that stopped the last batch because
	// it may still be written. Its settling period runs from when the worker
	// first saw it with this size and modification time, so a producer clock
	// that runs ahead cannot hold the inbox.
	Settling      string    `json:"settling,omitempty"`
	SettlingSize  int64     `json:"settling_size,omitempty"`
	SettlingMod   time.Time `json:"settling_mod,omitzero"`
	SettlingSince time.Time `json:"settling_since,omitzero"`
	// SettlingFile identifies the observed file where the platform allows it,
	// so a replacement with the same name, size and time settles anew.
	SettlingFile string `json:"settling_file,omitempty"`
}
