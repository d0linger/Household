package alerts

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestValidate(t *testing.T) {
	for name, c := range map[string]Config{
		"http ntfy":        {NtfyURL: "http://ntfy.example.org/topic"},
		"credentials":      {WebhookURL: "https://user:pw@hook.example.org/x"},
		"smtp port":        {SMTPAddress: "mail.example.org:25", SMTPFrom: "a@example.org", SMTPTo: []string{"b@example.org"}},
		"smtp from":        {SMTPAddress: "mail.example.org:587", SMTPFrom: "nope", SMTPTo: []string{"b@example.org"}},
		"smtp recipients":  {SMTPAddress: "mail.example.org:587", SMTPFrom: "a@example.org"},
		"smtp half login":  {SMTPAddress: "mail.example.org:587", SMTPFrom: "a@example.org", SMTPTo: []string{"b@example.org"}, SMTPUser: "u"},
		"token line break": {NtfyURL: "https://ntfy.example.org/t", NtfyToken: "a\nb"},
	} {
		if err := c.Validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	valid := Config{NtfyURL: "https://ntfy.example.org/household", WebhookURL: "https://hook.example.org/x",
		SMTPAddress: "mail.example.org:465", SMTPFrom: "household@example.org", SMTPTo: []string{"me@example.org"}, SMTPUser: "u", SMTPPassword: "p"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if (Config{}).Enabled() || !valid.Enabled() {
		t.Fatal("Enabled")
	}
}

func TestSendDeliversFixedTextToHTTPChannels(t *testing.T) {
	var ntfyBody, ntfyTitle, ntfyPriority, ntfyAuth string
	var hook map[string]any
	// The channels deliver concurrently; the captures are shared with the test.
	var captured sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Lock()
		defer captured.Unlock()
		if r.URL.Path == "/topic" {
			ntfyBody, ntfyTitle, ntfyPriority, ntfyAuth = string(body), r.Header.Get("Title"), r.Header.Get("Priority"), r.Header.Get("Authorization")
		} else {
			_ = json.Unmarshal(body, &hook)
		}
	}))
	defer server.Close()
	sender := NewSender(Config{NtfyURL: server.URL + "/topic", NtfyToken: "tk", WebhookURL: server.URL + "/hook", Installation: "Zuhause"})
	sender.Client = server.Client()
	if err := sender.Send(context.Background(), Message{Title: "Backup failed", Body: "Code: wrong_password", Critical: true}); err != nil {
		t.Fatal(err)
	}
	captured.Lock()
	defer captured.Unlock()
	if ntfyBody != "Code: wrong_password" || ntfyTitle != "Zuhause: Backup failed" || ntfyPriority != "high" || ntfyAuth != "Bearer tk" {
		t.Fatalf("ntfy = %q %q %q %q", ntfyBody, ntfyTitle, ntfyPriority, ntfyAuth)
	}
	if hook["title"] != "Zuhause: Backup failed" || hook["critical"] != true || hook["source"] != "household" {
		t.Fatalf("webhook = %v", hook)
	}
	failing := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer failing.Close()
	sender = NewSender(Config{NtfyURL: failing.URL + "/t"})
	sender.Client = failing.Client()
	if err := sender.Send(context.Background(), Message{Title: "x", Body: "y"}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("failing channel error = %v", err)
	}
	if err := (*Sender)(nil).Send(context.Background(), Message{}); err != nil {
		t.Fatal("nil sender must be a no-op")
	}
}

func TestMimeHeader(t *testing.T) {
	if got := mimeHeader("Backup ok"); got != "Backup ok" {
		t.Fatal(got)
	}
	decoder := new(mime.WordDecoder)
	for _, subject := range []string{"Sicherung überfällig\r\nBcc: x", "Haushalt " + strings.Repeat("ü", 40) + ": Sicherung überfällig"} {
		got := mimeHeader(subject)
		if !strings.HasPrefix(got, "=?UTF-8?q?") || strings.ContainsAny(got, "\r\n") {
			t.Fatalf("encoded = %q", got)
		}
		for _, word := range strings.Fields(got) {
			if len(word) > 75 {
				t.Fatalf("encoded word longer than 75 characters: %q", word)
			}
		}
		decoded, err := decoder.DecodeHeader(got)
		if want := strings.NewReplacer("\r", " ", "\n", " ").Replace(subject); err != nil || decoded != want {
			t.Fatalf("decoded = %q, %v; want %q", decoded, err, want)
		}
	}
}
