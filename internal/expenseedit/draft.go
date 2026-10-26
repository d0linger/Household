// Package expenseedit normalizes bounded EUR draft edits before persistence.
package expenseedit

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"household/internal/value"
)

const (
	LineLimit        = 100
	DescriptionLimit = 300
	PayeeLimit       = 200
	NoteLimit        = 300
)

type Problems map[string]string

func (p Problems) Error() string { return "Bitte die markierten Buchungsfelder prüfen" }

// HeaderInput uses an explicit euro input, unlike the generic API's numeric cents.
type HeaderInput struct {
	OccurredOn         string `json:"occurred_on"`
	Description        string `json:"description"`
	Payee              string `json:"payee"`
	AmountEUR          string `json:"amount_eur"`
	Kind               string `json:"kind"`
	PaidByMembershipID string `json:"paid_by_membership_id"`
}

type LineInput struct {
	Key        string `json:"key"`
	ID         string `json:"id"`
	CategoryID string `json:"category_id"`
	AmountEUR  string `json:"amount_eur"`
	Note       string `json:"note"`
	Version    int64  `json:"version"`
}

type Input struct {
	EntryID    string      `json:"entry_id"`
	Generation int64       `json:"generation"`
	Version    int64       `json:"version"`
	Action     string      `json:"action"`
	Header     HeaderInput `json:"header"`
	Lines      []LineInput `json:"lines"`
}

type Header struct {
	OccurredOn, Description, Payee, Kind, PaidByMembershipID string
	AmountMinor                                              int64
}

type Line struct {
	Key, ID, CategoryID, Note string
	Version, AmountMinor      int64
	Position                  int
}

type Draft struct {
	EntryID                  string
	Generation, Version      int64
	Action                   string
	Header                   Header
	Lines                    []Line
	SumMinor, RemainingMinor string
	ReadyToPost              bool
}

// Normalize never checks database identity, activity, visibility or saved row
// membership. The writer must establish those in its current transaction.
func Normalize(input Input) (Draft, error) {
	problems := Problems{}
	out := Draft{EntryID: strings.ToLower(input.EntryID), Generation: input.Generation, Version: input.Version,
		Action: input.Action, Lines: []Line{}}
	if out.Action != "save" && out.Action != "post" {
		problems["action"] = "Entwurf speichern oder ausdrücklich buchen"
	}
	if out.Generation <= 0 || out.EntryID == "" && out.Version != 0 ||
		out.EntryID != "" && (!value.ValidUUID(out.EntryID) || out.Version <= 0) {
		problems["version"] = "Aktuellen Buchungs- und Modulstand prüfen"
	}
	out.Header = Header{OccurredOn: input.Header.OccurredOn, Description: strings.TrimSpace(input.Header.Description),
		Payee: strings.TrimSpace(input.Header.Payee), Kind: input.Header.Kind, PaidByMembershipID: strings.ToLower(input.Header.PaidByMembershipID)}
	if out.Header.Kind == "" {
		out.Header.Kind = "expense"
	}
	if !value.Date(out.Header.OccurredOn) {
		problems["header.occurred_on"] = "Gültiges Buchungsdatum erforderlich"
	}
	if !boundedText(out.Header.Description, DescriptionLimit, true) {
		problems["header.description"] = "Beschreibung mit1bis300Zeichen erforderlich"
	}
	if !boundedText(out.Header.Payee, PayeeLimit, false) {
		problems["header.payee"] = "Empfänger auf200Zeichen begrenzen"
	}
	if out.Header.PaidByMembershipID != "" && !value.ValidUUID(out.Header.PaidByMembershipID) {
		problems["header.paid_by"] = "Gültiges Haushaltsmitglied auswählen oder Zahlenden offen lassen"
	}
	amount, err := euroMinor(input.Header.AmountEUR)
	if err != nil || amount == 0 || out.Header.Kind != "expense" && out.Header.Kind != "refund" ||
		out.Header.Kind == "expense" && amount < 0 || out.Header.Kind == "refund" && amount > 0 {
		problems["header.amount"] = "Positive Ausgabe oder negative Erstattung in exakten Euro und Cent erforderlich"
	}
	out.Header.AmountMinor = amount
	if len(input.Lines) > LineLimit {
		problems["lines"] = "Höchstens100Kategoriezeilen pro bearbeitbarem Entwurf"
		return Draft{}, problems
	}
	keys, ids := map[string]bool{}, map[string]bool{}
	var sum int64
	for index, raw := range input.Lines {
		line := Line{Key: strings.ToLower(raw.Key), ID: strings.ToLower(raw.ID), CategoryID: strings.ToLower(raw.CategoryID),
			Note: strings.TrimSpace(raw.Note), Version: raw.Version, Position: index}
		prefix := fmt.Sprintf("lines.%d.", index)
		if !value.ValidUUID(line.Key) || keys[line.Key] {
			problems[prefix+"key"] = "Eindeutige stabile Zeilenkennung erforderlich"
		}
		keys[line.Key] = true
		if line.ID == "" && line.Version != 0 || line.ID != "" && (!value.ValidUUID(line.ID) || line.Version <= 0 || ids[line.ID]) {
			problems[prefix+"version"] = "Gespeicherte Zeilenkennung und aktuellen Stand prüfen"
		}
		if line.ID != "" {
			ids[line.ID] = true
		}
		if !value.ValidUUID(line.CategoryID) {
			problems[prefix+"category"] = "Kategorie ausdrücklich auswählen"
		}
		if !boundedText(line.Note, NoteLimit, false) {
			problems[prefix+"note"] = "Zeilenbezeichnung auf300Zeichen begrenzen"
		}
		line.AmountMinor, err = euroMinor(raw.AmountEUR)
		if err != nil || line.AmountMinor == 0 {
			problems[prefix+"amount"] = "Exakten von null verschiedenen Euro-Teilbetrag eintragen"
		}
		// At most100 separately bounded +/-1e12-cent amounts cannot overflow int64.
		sum += line.AmountMinor
		out.Lines = append(out.Lines, line)
	}
	out.SumMinor = strconv.FormatInt(sum, 10)
	out.RemainingMinor = strconv.FormatInt(out.Header.AmountMinor-sum, 10)
	out.ReadyToPost = len(problems) == 0 && len(out.Lines) > 0 && out.Header.AmountMinor == sum
	if out.Action == "post" && !out.ReadyToPost {
		problems["total"] = "Vor dem Buchen vollständig aufteilen; Restdifferenz muss exakt null sein"
	}
	if len(problems) != 0 {
		return Draft{}, problems
	}
	return out, nil
}

func euroMinor(raw string) (int64, error) {
	if len(raw) > 32 {
		return 0, fmt.Errorf("EUR input exceeds its bounded form")
	}
	return value.Minor(raw)
}

func boundedText(text string, limit int, required bool) bool {
	return len(text) <= limit*utf8.UTFMax && utf8.ValidString(text) && !strings.ContainsRune(text, '\x00') &&
		(!required || text != "") && utf8.RuneCountInString(text) <= limit
}
