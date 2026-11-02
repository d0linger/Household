package document

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestMetadataRejectsIncompatibleAndInexactFields(t *testing.T) {
	for _, tc := range []struct {
		name, kind, fields string
		valid              bool
	}{
		{"exact invoice", "invoice", `{"total_minor":1299,"currency":"EUR","issued_on":"2026-11-01"}`, true},
		{"fractional cents", "invoice", `{"total_minor":12.99}`, false}, {"negative total", "invoice", `{"total_minor":-1}`, false},
		{"wrong type field", "manual", `{"total_minor":1299}`, false}, {"unknown field", "invoice", `{"sql":"x"}`, false},
		{"invalid date", "warranty", `{"purchased_on":"2026-02-30"}`, false}, {"bounded days", "contract", `{"notice_days":3661}`, false},
		{"zero exact amount", "invoice", `{"total_minor":0}`, true}, {"foreign currency", "invoice", `{"currency":"USD"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.fields), &fields); err != nil {
				t.Fatal(err)
			}
			err := (Metadata{Title: "Actual contract", Type: tc.kind, Fields: fields}).Validate()
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestRuleChangesProtectManualFactsAndUseLiteralMatching(t *testing.T) {
	rule := Rule{Name: "Power invoice", Contains: "Strom [2026]", Type: "invoice", Issuer: "Proposed supplier", Category: "Utilities", Enabled: true}
	out := RuleChanges(rule, "STROM [2026] bill", []string{"issuer", "document_type"})
	if len(out) != 1 || out["category"] != "Utilities" {
		t.Fatal(out)
	}
	if len(RuleChanges(rule, "Strom 2026 bill", nil)) != 0 {
		t.Fatal("rule interpreted as expression")
	}
	rule.Enabled = false
	if len(RuleChanges(rule, "Strom [2026]", nil)) != 0 {
		t.Fatal("disabled rule applied")
	}
}

func TestFiltersAndPagesRejectUnboundedOrAmbiguousInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		filters Filters
	}{
		{"unknown type", Filters{Type: "secret"}}, {"invalid date", Filters{DueBefore: "2026-13-01"}}, {"too long", Filters{Search: strings.Repeat("x", 201)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.filters.Validate() == nil {
				t.Fatal("invalid filter accepted")
			}
		})
	}
	if ValidatePages(nil) == nil || ValidatePages(make([]Page, 101)) == nil {
		t.Fatal("unbounded page selection")
	}
	if ValidatePages([]Page{{VersionID: strings.Repeat("a", 36), PageNumber: 0}}) == nil {
		t.Fatal("zero page accepted")
	}
}

func TestImagePDFProducesPortablePageAndRejectsCorruptInput(t *testing.T) {
	var original bytes.Buffer
	if err := png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 12, 9))); err != nil {
		t.Fatal(err)
	}
	pdf, err := imagePDF(original.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"%PDF-1.4", "/MediaBox [0 0 12 9]", "/DCTDecode", "xref", "%%EOF"} {
		if !bytes.Contains(pdf, []byte(part)) {
			t.Fatal("missing PDF structure", part)
		}
	}
	if _, err = imagePDF([]byte("corrupt")); err == nil {
		t.Fatal("corrupt source accepted")
	}
}
