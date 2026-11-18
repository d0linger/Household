package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFinanceClosedJSONRejectsAmbiguousAndIncompleteDocuments(t *testing.T) {
	type input struct {
		Name  string   `json:"name"`
		Value *string  `json:"value"`
		Rows  []string `json:"rows"`
	}
	var got input
	if err := DecodeFinanceDocument([]byte(`{"name":"Beleg","value":null,"rows":[]}`), &got); err != nil || got.Name != "Beleg" || got.Rows == nil {
		t.Fatal("closed document lost explicit null or empty array", got, err)
	}
	for _, raw := range []string{
		`{"Name":"Beleg","value":null,"rows":[]}`,
		`{"name":"A","name":"B","value":null,"rows":[]}`,
		`{"name":"A","\u006eame":"B","value":null,"rows":[]}`,
		`{"name":"A","value":null}`,
		`{"name":null,"value":null,"rows":[]}`,
		`{"name":"A","value":null,"rows":null}`,
		`{"name":"A","value":null,"rows":[],"extra":true}`,
		`{"name":"A","value":null,"rows":[]} {}`,
		`[]`, "{\"name\":\"\xff\",\"value\":null,\"rows\":[]}",
	} {
		if err := DecodeFinanceDocument([]byte(raw), &got); err == nil {
			t.Fatalf("ambiguous financial document accepted: %q", raw)
		}
	}
	var opaque json.RawMessage
	for _, raw := range []string{`{"a":{"x":1,"x":2}}`, strings.Repeat(`{"a":`, 18) + `0` + strings.Repeat("}", 18), `{"a":"` + strings.Repeat("x", financeBodyLimit) + `"}`} {
		if err := decodeFinancePayload([]byte(raw), &opaque); err == nil {
			t.Fatal("opaque payload escaped duplicate/depth/body limits")
		}
	}
}

func TestFinanceCommandHashPreservesReviewAndSourceBoundaries(t *testing.T) {
	base := FinanceCommand{Resource: "entries", Action: "save", ID: "00000000-0000-4000-8000-000000000001", Generation: 1,
		AccessVersion: strings.Repeat("a", 64), Payload: json.RawMessage(`{"money":{"minor":"9007199254740993","currency":"EUR"},"n":1}`)}
	hash, err := FinanceCommandHash(base)
	if err != nil || len(hash) != 64 {
		t.Fatal(hash, err)
	}
	reordered := base
	reordered.Payload = json.RawMessage(`{ "n":1, "money": { "currency":"EUR", "minor":"9007199254740993" } }`)
	reordered.ReviewToken, reordered.ReviewHash = "transport-proof", strings.Repeat("b", 64)
	if same, e := FinanceCommandHash(reordered); e != nil || same != hash {
		t.Fatal("semantic object ordering or transport proof changed command", same, e)
	}
	for _, change := range []func(*FinanceCommand){
		func(c *FinanceCommand) { c.Generation++ },
		func(c *FinanceCommand) { c.Action = "post" },
		func(c *FinanceCommand) { c.AccessVersion = strings.Repeat("b", 64) },
		func(c *FinanceCommand) {
			c.Payload = json.RawMessage(`{"money":{"minor":"9007199254740992","currency":"EUR"},"n":1}`)
		},
		func(c *FinanceCommand) {
			c.InitialAccess = &FinanceInitialAccess{AccessMode: "household", Grants: []FinanceInitialGrant{}}
		},
	} {
		changed := base
		change(&changed)
		if different, e := FinanceCommandHash(changed); e != nil || different == hash {
			t.Fatal("changed financial intent retained approval hash", different, e)
		}
	}
	for _, raw := range []string{`{} {}`, `{"a":1,"a":2}`, `null`, `[]`} {
		changed := base
		changed.Payload = json.RawMessage(raw)
		if _, e := FinanceCommandHash(changed); e == nil {
			t.Fatal("noncanonical raw payload was hashed", raw)
		}
	}
}

func TestFinanceBankDeclaredSyntaxAndDistinctSourceOccurrences(t *testing.T) {
	for _, tc := range []struct {
		raw, decimal, group string
		exponent            int
		want                int64
	}{
		{"-1.234,56", ",", ".", 2, -123456}, {"+1 234.500", ".", " ", 3, 1234500},
		{"123", ".", "", 0, 123}, {"0,01", ",", "", 2, 1}, {"10000000000.00", ".", "", 2, 1000000000000},
	} {
		got, err := financeParseBankDecimal(tc.raw, tc.exponent, tc.decimal, tc.group)
		if err != nil || got != tc.want {
			t.Fatal(tc.raw, got, err)
		}
	}
	for _, raw := range []string{"12.34,56", "1.234,567", "1e3", "--1,00", "1,", "10000000000,01", "1 000,00"} {
		if _, err := financeParseBankDecimal(raw, 2, ",", "."); err == nil {
			t.Fatal("undeclared/invalid decimal accepted", raw)
		}
	}
	profile := FinanceBankProfileInput{Name: "Native CSV", Delimiter: ";", Encoding: "utf-8", DateLayout: "dmy", DecimalSeparator: ",", AmountMode: "signed",
		FieldMap: FinanceBankFields{Date: "Tag", Amount: "Betrag", Payee: "Empfänger"}}
	account := "00000000-0000-4000-8000-000000000001"
	raw := []byte("\xef\xbb\xbfTag;Betrag;Empfänger\r\n30.10.2026;-1,00;\"Markt; Nord\"\r\n30.10.2026;-1,00;\"Markt; Nord\"\r\n")
	rows, hash, err := financeParseBankCSV(raw, profile, account, "EUR", 2)
	if err != nil || len(rows) != 2 || len(hash) != 64 || rows[0].AmountMinor != "-100" || rows[0].Payee != "Markt; Nord" || rows[0].Fingerprint == rows[1].Fingerprint || rows[1].Duplicate {
		t.Fatal("legitimate repeated transaction was discarded or inexact", rows, hash, err)
	}
	profile.FieldMap.ExternalID = "ID"
	strong := []byte("Tag;Betrag;Empfänger;ID\n30.10.2026;-1,00;Markt;bank-123\n31.10.2026;-2,00;Markt;bank-123\n")
	rows, _, err = financeParseBankCSV(strong, profile, account, "EUR", 2)
	if err != nil || len(rows) != 2 || !rows[1].Duplicate || rows[0].Fingerprint != rows[1].Fingerprint {
		t.Fatal("strong bank identity was not retained", rows, err)
	}
	other, _, err := financeParseBankCSV(strong, profile, "00000000-0000-4000-8000-000000000002", "EUR", 2)
	if err != nil || other[0].Fingerprint == rows[0].Fingerprint {
		t.Fatal("bank identity crossed accounts", err)
	}
	profile.FieldMap.ExternalID = ""
	for _, bad := range [][]byte{[]byte("Tag;Betrag;Empfänger\n" + strings.Repeat("30.10.2026;-1,00;M\n", 501)), []byte("Tag;Tag;Empfänger\n1;2;M"), []byte("Tag;Betrag;Empfänger\n\"offen"), []byte("a\x00b")} {
		if _, _, err = financeParseBankCSV(bad, profile, account, "EUR", 2); err == nil {
			t.Fatal("invalid/oversized CSV accepted")
		}
	}
}

func TestFinanceCivilRecurrenceDoesNotDriftAfterClamping(t *testing.T) {
	for _, tc := range []struct {
		anchor, after, through, unit, policy, want string
		interval                                   int
	}{
		{"2024-01-31", "2024-01-31", "2024-12-31", "month", "clamp", "2024-02-29", 1},
		{"2024-01-31", "2024-02-29", "2024-12-31", "month", "clamp", "2024-03-31", 1},
		{"2024-01-31", "2024-01-31", "2024-12-31", "month", "skip", "2024-03-31", 1},
		{"2024-02-29", "2024-02-29", "2028-12-31", "year", "skip", "2028-02-29", 1},
		{"2024-02-29", "2024-02-29", "2028-12-31", "year", "clamp", "2025-02-28", 1},
		{"2026-10-25", "2026-10-25", "2026-11-01", "day", "clamp", "2026-10-26", 1},
		{"2026-10-25", "2026-10-25", "2026-10-31", "week", "clamp", "", 1},
	} {
		got, err := financeNextRecurrence(tc.anchor, tc.after, tc.through, tc.unit, tc.interval, tc.policy)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := financeNextRecurrence("2026-02-30", "", "2026-12-31", "month", 1, "clamp"); err == nil {
		t.Fatal("invalid anchor normalized")
	}
}

func TestFinanceSelectionAndInitialAccessRemainClosedAndCopied(t *testing.T) {
	for _, q := range []FinanceQuery{
		{Pane: "accounts", Section: "cashflow"}, {Pane: "overview", Section: "receipts"},
		{Pane: "planning", Section: "contributions"}, {Pane: "entries", Goal: "00000000-0000-4000-8000-000000000001"},
		{Pane: "entries", Month: "1899-12"}, {Pane: "entries", From: "2026-02-30"},
	} {
		if validateFinanceQuery(q) == nil {
			t.Fatal("ambiguous selection accepted", q)
		}
	}
	from, to, err := financeDateWindow(FinanceQuery{Month: "2024-02"})
	if err != nil || from != "2024-02-01" || to != "2024-02-29" {
		t.Fatal(from, to, err)
	}
	from, to, err = financeDateWindow(FinanceQuery{Month: "2026-10", From: "2025-01-01", To: "2025-01-31"})
	if err != nil || from != "2025-01-01" || to != "2025-01-31" {
		t.Fatal("explicit range was broadened", from, to, err)
	}
	member := "original"
	ctx := withFinanceInitialAccess(context.Background(), "accounts", "bound", FinanceInitialAccess{AccessMode: "selected", ResponsibleMembershipID: &member, Grants: []FinanceInitialGrant{{MembershipID: "original", Permission: "edit"}}})
	member = "external-change"
	first := financeInitialAccessFor(ctx, "accounts", "bound")
	if first == nil || *first.ResponsibleMembershipID != "original" {
		t.Fatal("initial caller retained mutable authority pointer")
	}
	*first.ResponsibleMembershipID = "returned-change"
	first.Grants[0].Permission = "view"
	second := financeInitialAccessFor(ctx, "accounts", "bound")
	if *second.ResponsibleMembershipID != "original" || second.Grants[0].Permission != "edit" || financeInitialAccessFor(ctx, "entries", "bound") != nil {
		t.Fatal("returned authority escaped its exact target/value boundary")
	}
}

func TestFinanceCatchupBehindStopsAtTemplateEnd(t *testing.T) {
	today := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, last, endsOn string
		want               bool
	}{
		{"ended template with every occurrence materialized", "2026-09-01", "2026-09-01", false},
		{"ended template with an old gap before its end", "2026-08-01", "2026-09-01", true},
		{"open template keeps the old backlog check", "2026-09-01", "", true},
		{"open template inside the automatic window", "2026-11-01", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := financeCatchupBehind("2026-01-01", tc.last, tc.endsOn, today, "day", 1, "clamp")
			if err != nil || got != tc.want {
				t.Fatalf("financeCatchupBehind = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// financeNextRecurrenceFromAnchor is the previous search from index zero; the
// skip-ahead search must return exactly the same occurrences.
func financeNextRecurrenceFromAnchor(anchor, after, through, unit string, interval int, policy string) string {
	start, _ := time.Parse(time.DateOnly, anchor)
	for index := 0; index < 120000; index++ {
		var date time.Time
		switch unit {
		case "day":
			date = start.AddDate(0, 0, index*interval)
		case "week":
			date = start.AddDate(0, 0, index*interval*7)
		default:
			months := index * interval
			if unit == "year" {
				months *= 12
			}
			first := time.Date(start.Year(), start.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
			day, last := start.Day(), first.AddDate(0, 1, -1).Day()
			if day > last {
				if policy == "skip" {
					if first.Format(time.DateOnly) > through {
						return ""
					}
					continue
				}
				day = last
			}
			date = first.AddDate(0, 0, day-1)
		}
		if date.Year() > 2200 || date.Format(time.DateOnly) > through {
			return ""
		}
		if civil := date.Format(time.DateOnly); after == "" || civil > after {
			return civil
		}
	}
	return ""
}

func TestFinanceNextRecurrenceSkipsAheadWithIdenticalResults(t *testing.T) {
	anchors := []string{"1996-01-31", "2000-02-29", "2024-03-30", "2026-11-18"}
	afters := []string{"", "1995-06-01", "1996-01-31", "2001-02-28", "2024-02-29", "2026-11-17", "2030-12-31", "2199-12-30"}
	for _, anchor := range anchors {
		for _, after := range afters {
			for _, unit := range []string{"day", "week", "month", "year"} {
				for _, interval := range []int{1, 2, 3, 7} {
					for _, policy := range []string{"clamp", "skip"} {
						want := financeNextRecurrenceFromAnchor(anchor, after, "2200-12-31", unit, interval, policy)
						got, err := financeNextRecurrence(anchor, after, "2200-12-31", unit, interval, policy)
						if err != nil || got != want {
							t.Fatalf("%s after %s every %d %s (%s): got %q %v, want %q", anchor, after, interval, unit, policy, got, err, want)
						}
					}
				}
			}
		}
	}
	// A projection over a far-past daily anchor stays cheap: each lookup starts
	// next to its predecessor instead of at the anchor.
	last, count := "2026-01-01", 0
	for last != "" && count < 400 {
		next, err := financeNextRecurrence("1996-11-18", last, "2027-01-01", "day", 1, "clamp")
		if err != nil {
			t.Fatal(err)
		}
		last = next
		count++
	}
	if count != 366 {
		t.Fatalf("daily projection produced %d lookups, want 366", count)
	}
}
