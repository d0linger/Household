package ical

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TimedEvent exposes only a title and UTC schedule, never instructions,
// household members, materials or calendar capability secrets.
type TimedEvent struct {
	UID       string
	Start     time.Time
	End       time.Time
	Summary   string
	Sequence  int64
	Modified  time.Time
	Cancelled bool
	Completed bool
}

func MarshalTimed(events []TimedEvent) ([]byte, error) {
	if len(events) > 2000 {
		return nil, errors.New("calendar event limit exceeded")
	}
	var output strings.Builder
	line := func(text string) { fold(&output, text) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Household//Chores//DE")
	line("CALSCALE:GREGORIAN")
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if len(event.UID) == 0 || len(event.UID) > 200 || strings.IndexFunc(event.UID, func(r rune) bool { return r < 33 || r > 126 || strings.ContainsRune(":;,", r) }) >= 0 || seen[event.UID] ||
			!utf8.ValidString(event.Summary) || len(event.Summary) > 2048 || event.Sequence < 1 || event.Modified.IsZero() || event.Start.IsZero() ||
			!event.End.After(event.Start) || event.End.Sub(event.Start) > 24*time.Hour || event.Start.Year() < 1900 || event.Start.Year() > 2200 {
			return nil, errors.New("invalid timed calendar event")
		}
		seen[event.UID] = true
		line("BEGIN:VEVENT")
		line("UID:" + event.UID)
		line("DTSTAMP:" + event.Modified.UTC().Format("20060102T150405Z"))
		line("LAST-MODIFIED:" + event.Modified.UTC().Format("20060102T150405Z"))
		line("SEQUENCE:" + strconv.FormatInt(event.Sequence, 10))
		line("DTSTART:" + event.Start.UTC().Format("20060102T150405Z"))
		line("DTEND:" + event.End.UTC().Format("20060102T150405Z"))
		if event.Cancelled {
			line("SUMMARY:Abgesagte Haushaltsaufgabe")
			line("STATUS:CANCELLED")
		} else {
			line("SUMMARY:" + escape(event.Summary))
			line("STATUS:CONFIRMED")
		}
		if event.Completed {
			line("X-HOUSEHOLD-COMPLETED:TRUE")
		}
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return []byte(output.String()), nil
}
