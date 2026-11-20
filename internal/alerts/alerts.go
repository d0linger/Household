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
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"sync"
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
	// Every channel gets the whole deadline: a slow mail server must not
	// use up the time of ntfy or the webhook.
	var deliveries []func() error
	if s.Config.NtfyURL != "" {
		deliveries = append(deliveries, func() error { return s.ntfy(ctx, title, m) })
	}
	if s.Config.WebhookURL != "" {
		deliveries = append(deliveries, func() error { return s.webhook(ctx, title, m) })
	}
	if s.Config.SMTPAddress != "" {
		deliveries = append(deliveries, func() error { return s.mail(ctx, title, m) })
	}
	errs := make([]error, len(deliveries))
	var wait sync.WaitGroup
	for index, deliver := range deliveries {
		wait.Go(func() { errs[index] = deliver() })
	}
	wait.Wait()
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
	// The envelope takes bare addresses; display names stay in the headers.
	sender, err := mail.ParseAddress(s.Config.SMTPFrom)
	if err != nil {
		return errors.New("invalid sender address")
	}
	if err = client.Mail(sender.Address); err != nil {
		return errors.New("mail server refused the sender")
	}
	recipients := make([]string, 0, len(s.Config.SMTPTo))
	for _, to := range s.Config.SMTPTo {
		recipient, parseErr := mail.ParseAddress(to)
		if parseErr != nil {
			return errors.New("invalid recipient address")
		}
		if err = client.Rcpt(recipient.Address); err != nil {
			return errors.New("mail server refused a recipient")
		}
		// String encodes a non-ASCII display name as RFC 2047 words.
		recipients = append(recipients, recipient.String())
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("mail server refused the message")
	}
	message, err := composeMail(sender.String(), recipients, title, m.Body, time.Now())
	if err == nil {
		_, err = writer.Write(message)
	}
	if err != nil {
		return errors.New("mail delivery interrupted")
	}
	if err = writer.Close(); err != nil {
		return errors.New("mail server rejected the message")
	}
	return client.Quit()
}

// composeMail builds a 7-bit-safe message: encoded header words folded for
// the 76-character line limit and a quoted-printable body, so a relay
// without 8BITMIME or SMTPUTF8 accepts it.
func composeMail(from string, to []string, title, text string, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\n", from, strings.Join(to, ", "))
	b.WriteString(foldHeader("Subject", mimeHeader(title)))
	fmt.Fprintf(&b, "Date: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\nAuto-Submitted: auto-generated\r\n\r\n",
		now.Format(time.RFC1123Z))
	body := quotedprintable.NewWriter(&b)
	if _, err := body.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n") + "\r\n")); err != nil {
		return nil, err
	}
	if err := body.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// foldHeader writes "Name: value" and folds it between the space-separated
// words of value (encoded words included) so no line exceeds 76 characters.
func foldHeader(name, value string) string {
	var b strings.Builder
	line := name + ":"
	for _, word := range strings.Fields(value) {
		// Folding may also follow the colon, when the first word is long.
		if len(line)+1+len(word) > 76 {
			b.WriteString(line + "\r\n")
			line = ""
		}
		line += " " + word
	}
	b.WriteString(line + "\r\n")
	return b.String()
}

// mimeHeader encodes a non-ASCII subject as RFC 2047 UTF-8; the standard
// encoder splits long text into several encoded words of valid length.
func mimeHeader(value string) string {
	value = strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	return mime.QEncoding.Encode("UTF-8", value)
}
