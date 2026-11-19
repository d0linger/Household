// Package alerts sends optional operator warnings from the backup service to
// a channel outside Household: an ntfy topic, a generic webhook or an e-mail
// over SMTP. Messages carry fixed texts and public reason codes only, never
// household data, and a failed delivery never blocks the work it reports.
package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config selects the configured channels. Every field is optional.
type Config struct {
	NtfyURL      string // https://ntfy.example.org/topic
	NtfyToken    string
	WebhookURL   string // receives a small JSON document
	SMTPAddress  string // host:port, STARTTLS on 587 or implicit TLS on 465
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPTo       []string
	Installation string // a short label naming this installation in messages
}

// Enabled reports whether any channel is configured.
func (c Config) Enabled() bool { return c.NtfyURL != "" || c.WebhookURL != "" || c.SMTPAddress != "" }

// Variables lists the settings read by Load; `start` strips them so only the
// backup service holds channel credentials.
var Variables = []string{"HOUSEHOLD_ALERT_NTFY_URL", "HOUSEHOLD_ALERT_NTFY_TOKEN", "HOUSEHOLD_ALERT_WEBHOOK_URL",
	"HOUSEHOLD_ALERT_SMTP_ADDRESS", "HOUSEHOLD_ALERT_SMTP_USER", "HOUSEHOLD_ALERT_SMTP_PASSWORD", "HOUSEHOLD_ALERT_SMTP_FROM",
	"HOUSEHOLD_ALERT_SMTP_TO", "HOUSEHOLD_ALERT_NAME"}

// Load reads the alert channels. secret reads a value, its _FILE form or the
// secrets folder (config.Secret).
func Load(secret func(string) (string, error)) (Config, error) {
	var c Config
	var err error
	for name, target := range map[string]*string{"HOUSEHOLD_ALERT_NTFY_URL": &c.NtfyURL, "HOUSEHOLD_ALERT_NTFY_TOKEN": &c.NtfyToken,
		"HOUSEHOLD_ALERT_WEBHOOK_URL": &c.WebhookURL, "HOUSEHOLD_ALERT_SMTP_PASSWORD": &c.SMTPPassword} {
		if *target, err = secret(name); err != nil {
			return Config{}, err
		}
	}
	c.SMTPAddress = strings.TrimSpace(os.Getenv("HOUSEHOLD_ALERT_SMTP_ADDRESS"))
	c.SMTPUser = strings.TrimSpace(os.Getenv("HOUSEHOLD_ALERT_SMTP_USER"))
	c.SMTPFrom = strings.TrimSpace(os.Getenv("HOUSEHOLD_ALERT_SMTP_FROM"))
	c.Installation = strings.TrimSpace(os.Getenv("HOUSEHOLD_ALERT_NAME"))
	if c.Installation == "" {
		c.Installation = "Household"
	}
	if len(c.Installation) > 64 || strings.ContainsAny(c.Installation, "\r\n") {
		return Config{}, errors.New("HOUSEHOLD_ALERT_NAME must be one short line")
	}
	for _, to := range strings.Split(os.Getenv("HOUSEHOLD_ALERT_SMTP_TO"), ",") {
		if to = strings.TrimSpace(to); to != "" {
			c.SMTPTo = append(c.SMTPTo, to)
		}
	}
	return c, c.Validate()
}

// Validate checks every configured channel without contacting it.
func (c Config) Validate() error {
	for name, raw := range map[string]string{"HOUSEHOLD_ALERT_NTFY_URL": c.NtfyURL, "HOUSEHOLD_ALERT_WEBHOOK_URL": c.WebhookURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("%s must be an https URL without credentials", name)
		}
	}
	if strings.ContainsAny(c.NtfyToken, "\r\n") {
		return errors.New("HOUSEHOLD_ALERT_NTFY_TOKEN must be one line")
	}
	if c.SMTPAddress == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(c.SMTPAddress)
	if err != nil || host == "" || port != "465" && port != "587" {
		return errors.New("HOUSEHOLD_ALERT_SMTP_ADDRESS must be host:587 (STARTTLS) or host:465 (TLS)")
	}
	if _, err = mail.ParseAddress(c.SMTPFrom); err != nil {
		return errors.New("HOUSEHOLD_ALERT_SMTP_FROM must be an e-mail address")
	}
	if len(c.SMTPTo) == 0 || len(c.SMTPTo) > 10 {
		return errors.New("HOUSEHOLD_ALERT_SMTP_TO needs one to ten addresses")
	}
	for _, to := range c.SMTPTo {
		if _, err = mail.ParseAddress(to); err != nil {
			return errors.New("HOUSEHOLD_ALERT_SMTP_TO contains an invalid address")
		}
	}
	if (c.SMTPUser == "") != (c.SMTPPassword == "") {
		return errors.New("set both or neither of HOUSEHOLD_ALERT_SMTP_USER and HOUSEHOLD_ALERT_SMTP_PASSWORD")
	}
	return nil
}

// Message is one warning: a fixed title, a fixed body and a severity.
type Message struct {
	Title    string
	Body     string
	Critical bool
}

// Sender delivers messages to every configured channel.
type Sender struct {
	Config Config
	Client *http.Client
	// dial is replaced in tests.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewSender prepares a sender with bounded timeouts and TLS 1.2 or newer.
func NewSender(c Config) *Sender {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	return &Sender{Config: c, Client: &http.Client{Timeout: 20 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Send delivers m to every channel and joins their errors.
func (s *Sender) Send(ctx context.Context, m Message) error {
	if s == nil || !s.Config.Enabled() {
		return nil
	}
	title := s.Config.Installation + ": " + m.Title
	var errs []error
	if s.Config.NtfyURL != "" {
		errs = append(errs, s.ntfy(ctx, title, m))
	}
	if s.Config.WebhookURL != "" {
		errs = append(errs, s.webhook(ctx, title, m))
	}
	if s.Config.SMTPAddress != "" {
		errs = append(errs, s.mail(ctx, title, m))
	}
	return errors.Join(errs...)
}

func (s *Sender) post(ctx context.Context, target string, body []byte, headers map[string]string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	// security: the URL is operator configuration validated as https.
	response, err := s.Client.Do(request) // #nosec G107 G704 -- operator-configured alert channel
	if err != nil {
		return errors.New("alert channel unreachable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("alert channel answered %d", response.StatusCode)
	}
	return nil
}

func (s *Sender) ntfy(ctx context.Context, title string, m Message) error {
	headers := map[string]string{"Title": title, "Tags": "floppy_disk", "Content-Type": "text/plain; charset=utf-8"}
	if m.Critical {
		headers["Priority"] = "high"
	}
	if s.Config.NtfyToken != "" {
		headers["Authorization"] = "Bearer " + s.Config.NtfyToken
	}
	return s.post(ctx, s.Config.NtfyURL, []byte(m.Body), headers)
}

func (s *Sender) webhook(ctx context.Context, title string, m Message) error {
	body, err := json.Marshal(map[string]any{"title": title, "message": m.Body, "critical": m.Critical, "source": "household"})
	if err != nil {
		return err
	}
	return s.post(ctx, s.Config.WebhookURL, body, map[string]string{"Content-Type": "application/json"})
}

// mail sends a plain-text message. Port 465 uses implicit TLS, port 587
// requires STARTTLS; credentials are sent only over TLS.
func (s *Sender) mail(ctx context.Context, title string, m Message) error {
	host, port, _ := net.SplitHostPort(s.Config.SMTPAddress)
	dial := s.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	raw, err := dial(ctx, "tcp", s.Config.SMTPAddress)
	if err != nil {
		return errors.New("mail server unreachable")
	}
	_ = raw.SetDeadline(deadline)
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if port == "465" {
		raw = tls.Client(raw, tlsConfig)
	}
	client, err := smtp.NewClient(raw, host)
	if err != nil {
		_ = raw.Close()
		return errors.New("mail server handshake failed")
	}
	defer func() { _ = client.Close() }()
	if port == "587" {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return errors.New("mail server does not offer STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return errors.New("mail server TLS failed")
		}
	}
	if s.Config.SMTPUser != "" {
		if err = client.Auth(smtp.PlainAuth("", s.Config.SMTPUser, s.Config.SMTPPassword, host)); err != nil {
			return errors.New("mail server rejected the credentials")
		}
	}
	if err = client.Mail(s.Config.SMTPFrom); err != nil {
		return errors.New("mail server refused the sender")
	}
	for _, to := range s.Config.SMTPTo {
		if err = client.Rcpt(to); err != nil {
			return errors.New("mail server refused a recipient")
		}
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("mail server refused the message")
	}
	header := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\nAuto-Submitted: auto-generated\r\n\r\n",
		s.Config.SMTPFrom, strings.Join(s.Config.SMTPTo, ", "), mimeHeader(title), time.Now().Format(time.RFC1123Z))
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err = writer.Write([]byte(header + body + "\r\n")); err != nil {
		return errors.New("mail delivery interrupted")
	}
	if err = writer.Close(); err != nil {
		return errors.New("mail server rejected the message")
	}
	return client.Quit()
}

// mimeHeader encodes a non-ASCII subject as RFC 2047 UTF-8.
func mimeHeader(value string) string {
	value = strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	for _, r := range value {
		if r > 127 {
			return mimeEncode(value)
		}
	}
	return value
}

func mimeEncode(value string) string {
	var b strings.Builder
	b.WriteString("=?UTF-8?Q?")
	for _, c := range []byte(value) {
		switch {
		case c == ' ':
			b.WriteByte('_')
		case c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '-' || c == ':':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "=%02X", c)
		}
	}
	b.WriteString("?=")
	return b.String()
}
