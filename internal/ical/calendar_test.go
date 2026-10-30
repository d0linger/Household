package ical

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCalendarAllDayDatesAcrossDSTAndNewYear(t *testing.T) {
	for _, tc := range []struct{ date, end string }{
		{"2026-03-29", "20260330"}, {"2026-10-25", "20261026"}, {"2026-12-31", "20270101"}, {"2028-02-29", "20280301"},
	} {
		t.Run(tc.date, func(t *testing.T) {
			event := Event{UID: "meal@example.invalid", Date: tc.date, Summary: "Mittagessen", Sequence: 7,
				Modified: time.Date(2026, 10, 25, 3, 4, 5, 0, time.FixedZone("fixture", 3600))}
			raw, err := Marshal([]Event{event})
			if err != nil || !bytes.Contains(raw, []byte("DTEND;VALUE=DATE:"+tc.end+"\r\n")) ||
				!bytes.Contains(raw, []byte("LAST-MODIFIED:20261025T020405Z\r\n")) || bytes.Contains(raw, []byte("TZID")) {
				t.Fatalf("all-day date or UTC stamp changed: %q, %v", raw, err)
			}
		})
	}
}

func TestCalendarEscapesInjectionAndFoldsUTF8ByOctets(t *testing.T) {
	summary := strings.Repeat("Gemüse🥕", 40) + ",;\\\r\nBEGIN:VEVENT\nATTENDEE:private"
	raw, err := Marshal([]Event{{UID: "one@example.invalid", Date: "2026-10-30", Summary: summary, Sequence: 1, Modified: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(raw, []byte("\r\n")) {
		if len(line) > 75 || !utf8.Valid(line) {
			t.Fatalf("invalid physical line: %d bytes, validUTF8=%v", len(line), utf8.Valid(line))
		}
	}
	unfolded := strings.ReplaceAll(string(raw), "\r\n ", "")
	if strings.Count(unfolded, "\r\nBEGIN:VEVENT\r\n") != 1 || strings.Contains(unfolded, "\r\nATTENDEE:") ||
		!strings.Contains(unfolded, `\,\;\\\nBEGIN:VEVENT\nATTENDEE:private`) {
		t.Fatal("calendar text injection escaped the SUMMARY value", unfolded)
	}
	if bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("bare LF emitted")
	}
}

func TestCalendarCancellationContainsNoDeletedHumanText(t *testing.T) {
	raw, err := Marshal([]Event{{UID: "gone@example.invalid", Date: "2026-10-30", Summary: "Deleted confidential title", Sequence: 4, Modified: time.Now(), Cancelled: true}})
	if err != nil || bytes.Contains(raw, []byte("confidential")) || !bytes.Contains(raw, []byte("STATUS:CANCELLED\r\n")) ||
		!bytes.Contains(raw, []byte("SEQUENCE:4\r\n")) {
		t.Fatalf("cancellation privacy/revision failed: %s, %v", raw, err)
	}
}

func TestCalendarRejectsMalformedAndDuplicateEvents(t *testing.T) {
	valid := Event{UID: "valid@example.invalid", Date: "2026-10-30", Summary: "Dinner", Sequence: 1, Modified: time.Now()}
	for _, change := range []func(*Event){
		func(e *Event) { e.Date = "2026-02-30" }, func(e *Event) { e.Sequence = 0 },
		func(e *Event) { e.UID = "x\r\nBEGIN:VEVENT" }, func(e *Event) { e.Modified = time.Time{} },
		func(e *Event) { e.Summary = string([]byte{0xff}) }, func(e *Event) { e.Summary = strings.Repeat("a", 2049) },
	} {
		event := valid
		change(&event)
		if _, err := Marshal([]Event{event}); err == nil {
			t.Fatal("malformed event accepted")
		}
	}
	if _, err := Marshal([]Event{valid, valid}); err == nil {
		t.Fatal("duplicate UID accepted")
	}
	if _, err := Marshal(make([]Event, 555)); err == nil {
		t.Fatal("unbounded calendar accepted")
	}
}

func TestCalendarUIDChangesOnlyWithOwnedIdentity(t *testing.T) {
	uid := MealUID("household", 1, "meal")
	if uid != MealUID("household", 1, "meal") || uid == MealUID("household", 2, "meal") ||
		uid == MealUID("household", 1, "copied-meal") || uid == MealUID("other-household", 1, "meal") {
		t.Fatal("event identity is not stable and generation scoped")
	}
}
