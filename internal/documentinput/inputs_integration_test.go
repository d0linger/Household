//go:build integration

package documentinput

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDocumentInputsActualWatchAtomicReadyAndStableCursor(t *testing.T) {
	dir := t.TempDir()
	raw := []byte("%PDF-1.4\nwatch original\n")
	if err := os.WriteFile(filepath.Join(dir, "pending.pdf"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(dir, "first.ready.pdf")
	if err := os.WriteFile(ready, raw, 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(ready, past, past); err != nil {
		t.Fatal(err)
	}
	rows, cursor, err := Watch(context.Background(), dir, Cursor{})
	if err != nil || len(rows) != 1 || string(rows[0].Bytes) != string(raw) {
		t.Fatal(rows, cursor, err)
	}
	rows, _, err = Watch(context.Background(), dir, cursor)
	if err != nil || len(rows) != 0 {
		t.Fatal("cursor repeated page", rows, err)
	}
	if _, err = os.Stat(ready); err != nil {
		t.Fatal("source was moved or deleted", err)
	}
}

func TestDocumentInputsActualPaperlessTLSScopedOriginalAndRedirectRefusal(t *testing.T) {
	var requests []string
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.String())
		mu.Unlock()
		if r.Header.Get("Authorization") != "Token private-test-token" {
			t.Error("missing configured token")
		}
		switch r.URL.Path {
		case "/api/documents/":
			if r.URL.Query().Get("tags__id__all") != "17" {
				t.Error("missing explicit scope")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"next":null,"results":[{"id":42,"title":"Original receipt","original_file_name":"receipt.pdf"}]}`)
		case "/api/documents/42/download/":
			if r.URL.Query().Get("original") != "true" {
				t.Error("archive rendition requested")
			}
			_, _ = io.WriteString(w, "%PDF-1.4\nexact source\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	cfg := Config{PaperlessURL: server.URL, PaperlessToken: "private-test-token", PaperlessTag: 17, RootCAs: roots}
	rows, cursor, err := Paperless(context.Background(), cfg, Cursor{})
	if err != nil || len(rows) != 1 || rows[0].Key != "42" || string(rows[0].Bytes) != "%PDF-1.4\nexact source\n" || cursor.Page != 1 {
		t.Fatal(rows, cursor, err)
	}
	untrusted := cfg
	untrusted.RootCAs = nil
	if _, _, err = Paperless(context.Background(), untrusted, Cursor{}); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatal(requests)
	}
}

func TestDocumentInputsActualIMAPTLSReadOnlyUIDIdentity(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := certificateServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certificateServer.Certificate())
	certificateServer.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	commands := make(chan []string, 1)
	problems := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			problems <- e
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = io.WriteString(conn, "* OK isolated read-only IMAP\r\n")
		reader := bufio.NewReader(conn)
		seen := []string{}
		for n := 0; n < 4; n++ {
			line, e := reader.ReadString('\n')
			if e != nil {
				problems <- e
				return
			}
			line = strings.TrimSpace(line)
			tag, command, _ := strings.Cut(line, " ")
			seen = append(seen, command)
			switch {
			case strings.HasPrefix(command, "LOGIN "):
				_, _ = fmt.Fprintf(conn, "%s OK login\r\n", tag)
			case strings.HasPrefix(command, "EXAMINE "):
				_, _ = fmt.Fprintf(conn, "* OK [UIDVALIDITY 77] identity\r\n%s OK [READ-ONLY] selected\r\n", tag)
			case strings.HasPrefix(command, "UID SEARCH "):
				_, _ = fmt.Fprintf(conn, "* SEARCH 5\r\n%s OK search\r\n", tag)
			case command == "UID FETCH 5 (UID BODY.PEEK[])":
				body := "MIME-Version: 1.0\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=mail.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\nmail original\n"))
				_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 5 BODY[] {%d}\r\n%s)\r\n%s OK fetched\r\n", len(body), body, tag)
			default:
				problems <- fmt.Errorf("unexpected IMAP mutation or command: %s", command)
				return
			}
		}
		commands <- seen
		problems <- nil
	}()
	address := listener.Addr().(*net.TCPAddr)
	cfg := Config{IMAPAddress: fmt.Sprintf("127.0.0.1:%d", address.Port), IMAPUser: "reader", IMAPPassword: "test-password", IMAPMailbox: "Household", RootCAs: roots}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	rows, cursor, err := IMAP(ctx, cfg, Cursor{})
	if err != nil || len(rows) != 1 || rows[0].Key != "77/5/1" || cursor.LastUID != 5 || string(rows[0].Bytes) != "%PDF-1.4\nmail original\n" {
		t.Fatal(rows, cursor, err)
	}
	if err = <-problems; err != nil {
		t.Fatal(err)
	}
	for _, command := range <-commands {
		if strings.Contains(command, "STORE") || strings.Contains(command, "EXPUNGE") || strings.HasPrefix(command, "SELECT ") {
			t.Fatal("mailbox mutation", command)
		}
	}
}
