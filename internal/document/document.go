// Package document defines bounded, deterministic document review contracts.
package document

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var Types = []string{"other", "invoice", "warranty", "contract", "identity", "manual"}
var DeadlineKinds = []string{"return", "warranty", "expiry", "cancellation", "payment", "custom"}

type Metadata struct {
	Title    string                     `json:"title"`
	Category string                     `json:"category"`
	Issuer   string                     `json:"issuer"`
	Notes    string                     `json:"notes"`
	Type     string                     `json:"document_type"`
	Fields   map[string]json.RawMessage `json:"fields"`
	Reviewed bool                       `json:"reviewed"`
}

func (m Metadata) Validate() error {
	if strings.TrimSpace(m.Title) == "" || utf8.RuneCountInString(m.Title) > 200 ||
		utf8.RuneCountInString(m.Category) > 200 || utf8.RuneCountInString(m.Issuer) > 200 ||
		utf8.RuneCountInString(m.Notes) > 10000 || !slices.Contains(Types, m.Type) || len(m.Fields) > 12 {
		return errors.New("titel, Dokumenttyp und begrenzte Metadaten prüfen")
	}
	allowed := map[string]string{"reference": "text"}
	switch m.Type {
	case "invoice":
		allowed["invoice_number"] = "text"
		allowed["total_minor"] = "money"
		allowed["currency"] = "currency"
		allowed["issued_on"] = "date"
	case "warranty":
		allowed["serial_number"] = "text"
		allowed["manufacturer"] = "text"
		allowed["purchased_on"] = "date"
	case "contract":
		allowed["contract_number"] = "text"
		allowed["starts_on"] = "date"
		allowed["notice_days"] = "days"
	case "identity":
		allowed["identifier"] = "text"
		allowed["issued_on"] = "date"
	case "manual":
		allowed["model"] = "text"
		allowed["manufacturer"] = "text"
	}
	for key, raw := range m.Fields {
		kind, ok := allowed[key]
		if !ok || len(raw) > 1000 || strings.TrimSpace(string(raw)) == "null" {
			return errors.New("zusatzfeld gehört nicht zum gewählten Dokumenttyp oder ist leer")
		}
		if kind == "money" || kind == "days" {
			var v int64
			if json.Unmarshal(raw, &v) != nil || v < 0 || v > 9000000000000000 || kind == "days" && v > 3660 {
				return errors.New("ganzzahligen Betrag beziehungsweise Frist prüfen")
			}
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) != nil || utf8.RuneCountInString(v) > 200 {
			return errors.New("zusatzfeld muss ein begrenzter Text sein")
		}
		if kind == "date" {
			if _, err := time.Parse(time.DateOnly, v); err != nil {
				return errors.New("datum im Format JJJJ-MM-TT angeben")
			}
		}
		if kind == "currency" && v != "EUR" {
			return errors.New("währung muss EUR sein")
		}
	}
	return nil
}

type Filters struct {
	Search    string `json:"search"`
	Type      string `json:"document_type"`
	State     string `json:"state"`
	Issuer    string `json:"issuer"`
	Category  string `json:"category"`
	DueBefore string `json:"due_before"`
	Cursor    string `json:"cursor"`
}

func (f Filters) Validate() error {
	if utf8.RuneCountInString(f.Search) > 200 || utf8.RuneCountInString(f.Issuer) > 200 || utf8.RuneCountInString(f.Category) > 200 ||
		f.Type != "" && !slices.Contains(Types, f.Type) || !slices.Contains([]string{"", "unreviewed", "reviewed", "needs_attention", "failed", "archived"}, f.State) {
		return errors.New("dokumentfilter prüfen")
	}
	if f.DueBefore != "" {
		if _, err := time.Parse(time.DateOnly, f.DueBefore); err != nil {
			return errors.New("fristenfilter prüfen")
		}
	}
	return nil
}

type Page struct {
	VersionID  string `json:"version_id"`
	PageNumber int    `json:"page_number"`
}

func ValidatePages(pages []Page) error {
	if len(pages) == 0 || len(pages) > 100 {
		return errors.New("zwischen 1 und 100 Seiten auswählen")
	}
	for _, p := range pages {
		if len(p.VersionID) != 36 || p.PageNumber < 1 || p.PageNumber > 100 {
			return errors.New("seitenquelle und Seitennummer prüfen")
		}
	}
	return nil
}

type Rule struct {
	Name     string `json:"name"`
	Contains string `json:"contains_text"`
	Type     string `json:"document_type"`
	Issuer   string `json:"issuer"`
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
}

func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" || utf8.RuneCountInString(r.Name) > 100 || utf8.RuneCountInString(strings.TrimSpace(r.Contains)) < 2 || utf8.RuneCountInString(r.Contains) > 200 || !slices.Contains(Types, r.Type) || utf8.RuneCountInString(r.Issuer) > 200 || utf8.RuneCountInString(r.Category) > 200 {
		return errors.New("regelname, Textbedingung und Zieltyp prüfen")
	}
	return nil
}

func RuleChanges(r Rule, source string, manual []string) map[string]string {
	out := map[string]string{}
	if !r.Enabled || !strings.Contains(strings.ToLower(source), strings.ToLower(r.Contains)) {
		return out
	}
	for key, v := range map[string]string{"document_type": r.Type, "issuer": r.Issuer, "category": r.Category} {
		if v != "" && !slices.Contains(manual, key) {
			out[key] = v
		}
	}
	return out
}
