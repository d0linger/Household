package maintenance

import "testing"

func TestPurgeRejectsInvalidRequestBeforeTransaction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, request string }{
		{"missing", ""},
		{"module name", "shopping"},
		{"SQL text", "'; TRUNCATE core.users; --"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := Purge(t.Context(), nil, tc.request); err == nil {
				t.Fatal("invalid purge request accepted")
			}
		})
	}
}
