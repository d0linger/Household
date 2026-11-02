package documentinput

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func Paperless(ctx context.Context, c Config, cursor Cursor) ([]Candidate, Cursor, error) {
	if err := c.Validate(); err != nil {
		return nil, cursor, err
	}
	u, err := url.Parse(c.PaperlessURL)
	if err != nil || u.Host == "" {
		return nil, cursor, errors.New("paperless not configured")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: c.TLS(u.Hostname()), ResponseHeaderTimeout: 8 * time.Second, MaxConnsPerHost: 1, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("document input redirects refused") }}
	get := func(path string, limit int64) ([]byte, http.Header, error) {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.PaperlessURL, "/")+path, nil)
		if e != nil {
			return nil, nil, e
		}
		req.Header.Set("Authorization", "Token "+c.PaperlessToken)
		res, e := client.Do(req)
		if e != nil {
			return nil, nil, errors.New("paperless connection failed")
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return nil, nil, errors.New("paperless rejected read request")
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
		if e != nil || int64(len(raw)) > limit {
			return nil, nil, errors.New("paperless response exceeds limit")
		}
		return raw, res.Header, nil
	}
	page := cursor.Page
	if page < 1 || page > 10000 {
		page = 1
	}
	raw, _, err := get("/api/documents/?page_size=10&ordering=id&page="+strconv.Itoa(page)+"&tags__id__all="+strconv.Itoa(c.PaperlessTag), 1<<20)
	if err != nil {
		return nil, cursor, err
	}
	var response struct {
		Next    *string `json:"next"`
		Results []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Name  string `json:"original_file_name"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Results) > 10 {
		return nil, cursor, errors.New("invalid bounded paperless inventory")
	}
	out := []Candidate{}
	for _, item := range response.Results {
		if item.ID < 1 {
			return nil, cursor, errors.New("invalid paperless document identity")
		}
		body, header, e := get("/api/documents/"+strconv.FormatInt(item.ID, 10)+"/download/?original=true", 10<<20)
		if e != nil {
			return nil, cursor, e
		}
		name := item.Name
		if _, params, e := mime.ParseMediaType(header.Get("Content-Disposition")); e == nil && params["filename"] != "" {
			name = params["filename"]
		}
		if name == "" {
			name = item.Title + ".pdf"
		}
		out = append(out, Candidate{Key: strconv.FormatInt(item.ID, 10), Name: name, Bytes: body})
	}
	next := cursor
	next.Page = 1
	if response.Next != nil {
		next.Page = page + 1
	}
	return out, next, nil
}
