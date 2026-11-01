package ical

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTimedCalendarEscapesFoldsAndPreservesOccurrenceIdentity(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	event := TimedEvent{UID: "chores-test-g1-occurrence@household.local", Start: now, End: now.Add(15 * time.Minute), Sequence: 2, Modified: now, Summary: strings.Repeat("Äpfel", 30) + "\nBEGIN:VEVENT,;\\"}
	raw, err := MarshalTimed([]TimedEvent{event})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("DTSTART:20261101T090000Z\r\n")) || !bytes.Contains(raw, []byte("\\nBEGIN:VEVENT\\,\\;\\\\")) {
		t.Fatal("UTC/escaping", string(raw))
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 75 || !utf8.ValidString(line) {
			t.Fatal("invalid folded line", line)
		}
	}
	event.Cancelled = true
	event.Sequence++
	cancelled, err := MarshalTimed([]TimedEvent{event})
	if err != nil || !bytes.Contains(cancelled, []byte("STATUS:CANCELLED")) || bytes.Contains(cancelled, []byte("Äpfel")) || !bytes.Contains(cancelled, []byte("UID:"+event.UID)) {
		t.Fatal("cancellation lost identity or exposed title", err)
	}
	if _, err = MarshalTimed([]TimedEvent{event, event}); err == nil {
		t.Fatal("duplicate UID accepted")
	}
}
