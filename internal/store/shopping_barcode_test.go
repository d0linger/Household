package store

import (
	"strings"
	"testing"
)

func TestShoppingBarcodeExactLocalIdentity(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
	}{
		{"leading zeros", "  0012345678905\r\n", "0012345678905"},
		{"internal code", "00073AbC", "00073AbC"},
		{"case remains distinct", "00073abc", "00073abc"},
		{"inert URL", "https://example.invalid/a?x=1&y=2", "https://example.invalid/a?x=1&y=2"},
		{"inert markup", "<script>alert(1)</script>", "<script>alert(1)</script>"},
		{"64 bytes", strings.Repeat("A", 64), strings.Repeat("A", 64)},
		{"unicode outer whitespace", "\u00a0Code\u00a0", "Code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := shoppingBarcodeCode(test.raw)
			if err != nil || got != test.want {
				t.Fatalf("exact identity %q, %v", got, err)
			}
		})
	}
}

func TestShoppingBarcodeRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	for _, test := range []struct{ name, raw string }{
		{"empty", ""}, {"whitespace", " \n "}, {"65 bytes", strings.Repeat("A", 65)},
		{"interior space", "two codes"}, {"control", "one\ntwo"}, {"NUL", "one\x00"},
		{"DEL", "one\x7f"}, {"non ASCII", "Milchä"}, {"unbounded trim", strings.Repeat(" ", 300) + "A"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := shoppingBarcodeCode(test.raw); err == nil {
				t.Fatal("unsupported identity accepted")
			}
		})
	}
}
