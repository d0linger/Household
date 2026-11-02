package documentinput

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var imapLiteral = regexp.MustCompile(`\{([0-9]{1,9})\}$`)
var imapValidity = regexp.MustCompile(`\[UIDVALIDITY ([0-9]{1,20})\]`)
var imapFetchedUID = regexp.MustCompile(`\bUID ([0-9]{1,10})\b`)

type imapReader struct {
	conn     net.Conn
	reader   *bufio.Reader
	sequence int
}

func (c *imapReader) line() (string, error) {
	line, e := c.reader.ReadSlice('\n')
	if e != nil || len(line) > 8192 {
		return "", errors.New("IMAP response line limit reached")
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), nil
}
func (c *imapReader) command(command string) ([]string, [][]byte, error) {
	c.sequence++
	tag := fmt.Sprintf("H%04d", c.sequence)
	if _, err := io.WriteString(c.conn, tag+" "+command+"\r\n"); err != nil {
		return nil, nil, errors.New("IMAP write failed")
	}
	lines := []string{}
	literals := [][]byte{}
	read := 0
	for n := 0; n < 300; n++ {
		line, err := c.line()
		if err != nil {
			return nil, nil, err
		}
		read += len(line)
		if read > 18<<20 {
			return nil, nil, errors.New("IMAP response limit reached")
		}
		if strings.HasPrefix(line, tag+" ") {
			if !strings.HasPrefix(line, tag+" OK") {
				return nil, nil, errors.New("IMAP read request rejected")
			}
			return lines, literals, nil
		}
		lines = append(lines, line)
		if match := imapLiteral.FindStringSubmatch(line); match != nil {
			size, err := strconv.Atoi(match[1])
			if err != nil || size > 15<<20 || size < 0 || len(literals) >= 1 {
				return nil, nil, errors.New("IMAP message limit reached")
			}
			body := make([]byte, size)
			if _, err = io.ReadFull(c.reader, body); err != nil {
				return nil, nil, errors.New("IMAP literal truncated")
			}
			read += size
			literals = append(literals, body)
		}
	}
	return nil, nil, errors.New("IMAP response count limit reached")
}

func imapQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func IMAP(ctx context.Context, c Config, cursor Cursor) ([]Candidate, Cursor, error) {
	if err := c.Validate(); err != nil {
		return nil, cursor, err
	}
	host, _, err := net.SplitHostPort(c.IMAPAddress)
	if err != nil {
		return nil, cursor, errors.New("IMAP not configured")
	}
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: c.TLS(host)}
	conn, err := dialer.DialContext(ctx, "tcp", c.IMAPAddress)
	if err != nil {
		return nil, cursor, errors.New("IMAP TLS connection failed")
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err = conn.SetDeadline(time.Now().Add(35 * time.Second)); err != nil {
		return nil, cursor, err
	}
	client := imapReader{conn: conn, reader: bufio.NewReaderSize(conn, 8193)}
	greeting, err := client.line()
	if err != nil || !strings.HasPrefix(greeting, "* OK") {
		return nil, cursor, errors.New("IMAP greeting rejected")
	}
	if _, _, err = client.command("LOGIN " + imapQuote(c.IMAPUser) + " " + imapQuote(c.IMAPPassword)); err != nil {
		return nil, cursor, errors.New("IMAP authentication failed")
	}
	lines, _, err := client.command("EXAMINE " + imapQuote(c.IMAPMailbox))
	if err != nil {
		return nil, cursor, err
	}
	var validity uint64
	for _, line := range lines {
		if match := imapValidity.FindStringSubmatch(line); match != nil {
			validity, _ = strconv.ParseUint(match[1], 10, 64)
		}
	}
	if validity == 0 {
		return nil, cursor, errors.New("IMAP UID validity required")
	}
	next := cursor
	if next.UIDValidity != validity {
		next.UIDValidity = validity
		next.LastUID = 0
	}
	if next.LastUID == ^uint64(0) {
		return nil, cursor, errors.New("IMAP UID overflow")
	}
	lines, _, err = client.command("UID SEARCH UID " + strconv.FormatUint(next.LastUID+1, 10) + ":*")
	if err != nil {
		return nil, cursor, err
	}
	uids := []uint64{}
	for _, line := range lines {
		if !strings.HasPrefix(line, "* SEARCH") {
			continue
		}
		for _, token := range strings.Fields(strings.TrimPrefix(line, "* SEARCH")) {
			uid, e := strconv.ParseUint(token, 10, 32)
			if e != nil || uid == 0 {
				return nil, cursor, errors.New("invalid IMAP UID inventory")
			}
			if uid > next.LastUID {
				uids = append(uids, uid)
			}
		}
	}
	slices.Sort(uids)
	uids = slices.Compact(uids)
	if len(uids) > 3 {
		uids = uids[:3]
	}
	out := []Candidate{}
	total := 0
	for _, uid := range uids {
		fetched, literal, e := client.command("UID FETCH " + strconv.FormatUint(uid, 10) + " (UID BODY.PEEK[])")
		if e != nil {
			return nil, cursor, e
		}
		match := imapFetchedUID.FindStringSubmatch(strings.Join(fetched, " "))
		if len(literal) != 1 || match == nil || match[1] != strconv.FormatUint(uid, 10) {
			return nil, cursor, errors.New("IMAP message body or requested UID missing")
		}
		attachments, e := messageAttachments(literal[0])
		if e != nil {
			return nil, cursor, e
		}
		for i, a := range attachments {
			total += len(a.Bytes)
			if len(out) >= 10 || total > 30<<20 {
				return nil, cursor, errors.New("IMAP attachment batch limit reached")
			}
			a.Key = strconv.FormatUint(validity, 10) + "/" + strconv.FormatUint(uid, 10) + "/" + strconv.Itoa(i+1)
			out = append(out, a)
		}
		next.LastUID = uid
	}
	return out, next, nil
}

func messageAttachments(raw []byte) ([]Candidate, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid MIME message")
	}
	out := []Candidate{}
	parts := 0
	var visit func(mapHeader map[string][]string, body io.Reader, depth int) error
	visit = func(header map[string][]string, body io.Reader, depth int) error {
		parts++
		if depth > 6 || parts > 40 {
			return errors.New("MIME nesting or part limit reached")
		}
		get := func(k string) string {
			v := header[k]
			if len(v) > 0 {
				return v[0]
			}
			return ""
		}
		contentType := get("Content-Type")
		if contentType == "" {
			contentType = "text/plain"
		}
		media, params, e := mime.ParseMediaType(contentType)
		if e != nil {
			return errors.New("invalid MIME content type")
		}
		if strings.HasPrefix(media, "multipart/") {
			reader := multipart.NewReader(body, params["boundary"])
			for {
				part, e := reader.NextRawPart()
				if errors.Is(e, io.EOF) {
					return nil
				}
				if e != nil {
					return errors.New("invalid MIME multipart")
				}
				e = visit(part.Header, part, depth+1)
				_ = part.Close()
				if e != nil {
					return e
				}
			}
		}
		disposition, attributes, _ := mime.ParseMediaType(get("Content-Disposition"))
		name := attributes["filename"]
		if name == "" {
			name = params["name"]
		}
		if disposition != "attachment" && name == "" {
			return nil
		}
		if !slices.Contains([]string{"application/pdf", "image/jpeg", "image/png"}, media) {
			return nil
		}
		switch strings.ToLower(get("Content-Transfer-Encoding")) {
		case "base64":
			body = base64.NewDecoder(base64.StdEncoding, body)
		case "quoted-printable":
			body = quotedprintable.NewReader(body)
		case "", "7bit", "8bit", "binary":
		default:
			return errors.New("unsupported MIME transfer encoding")
		}
		data, e := io.ReadAll(io.LimitReader(body, (10<<20)+1))
		if e != nil || len(data) == 0 || len(data) > 10<<20 {
			return errors.New("MIME attachment limit reached")
		}
		if name == "" {
			name = "Mail-Anhang"
		}
		out = append(out, Candidate{Name: name, Bytes: data})
		return nil
	}
	err = visit(message.Header, message.Body, 0)
	return out, err
}
