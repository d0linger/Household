// Package ical writes the deliberately small all-day Mealplan calendar profile.
package ical

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Event contains only the fields explicitly shared by the calendar capability.
type Event struct {
	UID       string
	Date      string
	Summary   string
	Sequence  int64
	Modified  time.Time
	Cancelled bool
}

// Marshal emits RFC5545 DATE events without local clock times or private notes.
func Marshal(events []Event) ([]byte, error) {
	if len(events) > 554 {
		return nil, errors.New("calendar event limit exceeded")
	}
	var output strings.Builder
	line := func(value string) { fold(&output, value) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Household//Mealplan//DE")
	line("CALSCALE:GREGORIAN")
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		date, err := time.Parse(time.DateOnly, event.Date)
		if err != nil || date.Year() < 1 || date.Year() >= 9999 || event.Sequence < 1 || event.Modified.IsZero() ||
			len(event.UID) == 0 || len(event.UID) > 200 || strings.ContainsAny(event.UID, "\r\n:;, ") ||
			strings.IndexFunc(event.UID, func(r rune) bool { return r < 33 || r > 126 }) >= 0 ||
			!utf8.ValidString(event.UID) || !utf8.ValidString(event.Summary) || len(event.Summary) > 2048 || seen[event.UID] {
			return nil, errors.New("invalid calendar event")
		}
		seen[event.UID] = true
		line("BEGIN:VEVENT")
		line("UID:" + event.UID)
		line("DTSTAMP:" + event.Modified.UTC().Format("20060102T150405Z"))
		line("LAST-MODIFIED:" + event.Modified.UTC().Format("20060102T150405Z"))
		line("SEQUENCE:" + strconv.FormatInt(event.Sequence, 10))
		line("DTSTART;VALUE=DATE:" + date.Format("20060102"))
		line("DTEND;VALUE=DATE:" + date.AddDate(0, 0, 1).Format("20060102"))
		if event.Cancelled {
			line("SUMMARY:Abgesagte Mahlzeit")
			line("STATUS:CANCELLED")
		} else {
			line("SUMMARY:" + escape(event.Summary))
			line("STATUS:CONFIRMED")
		}
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return []byte(output.String()), nil
}

func escape(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	var output strings.Builder
	for _, char := range value {
		switch char {
		case '\\', ';', ',':
			output.WriteByte('\\')
			output.WriteRune(char)
		case '\n':
			output.WriteString(`\n`)
		default:
			if char < 32 || char == 127 {
				output.WriteByte(' ')
			} else {
				output.WriteRune(char)
			}
		}
	}
	return output.String()
}

// Continuation whitespace counts toward the75-octet physical line limit. Move
// the cut backward to a UTF-8 rune boundary rather than splitting a code point.
func fold(output *strings.Builder, value string) {
	limit := 75
	for len(value) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		output.WriteString(value[:cut])
		output.WriteString("\r\n ")
		value = value[cut:]
		limit = 74
	}
	output.WriteString(value)
	output.WriteString("\r\n")
}

// MealUID is independent of a feed token and changes after a module purge.
func MealUID(household string, generation int64, meal string) string {
	return fmt.Sprintf("mealplan-%s-g%d-%s@household.local", household, generation, meal)
}
