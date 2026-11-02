package documentinput

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMessageAttachmentsDecodeAndRejectOversizedOrCorruptMIME(t *testing.T) {
	original := []byte("%PDF-1.4\noriginal bytes\n")
	message := "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=HH\r\n\r\n--HH\r\nContent-Type: text/plain\r\n\r\nUntrusted text\r\n--HH\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=Receipt.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(original) + "\r\n--HH--\r\n"
	attachments, err := messageAttachments([]byte(message))
	if err != nil || len(attachments) != 1 || string(attachments[0].Bytes) != string(original) {
		t.Fatal(attachments, err)
	}
	if _, err = messageAttachments([]byte("not a valid MIME header\n")); err == nil {
		t.Fatal("corrupt MIME accepted")
	}
	giant := strings.Replace(message, base64.StdEncoding.EncodeToString(original), base64.StdEncoding.EncodeToString(make([]byte, (10<<20)+1)), 1)
	if _, err = messageAttachments([]byte(giant)); err == nil {
		t.Fatal("oversized attachment accepted")
	}
}

func TestInputConfigurationRequiresTLSAndExplicitScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Config
	}{
		{"relative folder", Config{WatchDir: "../mail"}}, {"incomplete IMAP", Config{IMAPAddress: "example.test:993"}},
		{"HTTP Paperless", Config{PaperlessURL: "http://example.test", PaperlessToken: "token", PaperlessTag: 1}},
		{"unscoped Paperless", Config{PaperlessURL: "https://example.test", PaperlessToken: "token"}},
		{"credential newline", Config{PaperlessToken: "token\r\ninjected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.c.Validate() == nil {
				t.Fatal("unsafe input configuration accepted")
			}
		})
	}
	if (Config{}).Validate() != nil {
		t.Fatal("default integrations must remain disabled")
	}
}
