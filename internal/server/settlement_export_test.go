package server

import "testing"

func TestSettlementKindLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, expected string
	}{
		{name: "payment", kind: "payment", expected: "Zahlung"},
		{name: "credit", kind: "credit", expected: "Gutschrift"},
		{name: "offset", kind: "offset", expected: "Verrechnung"},
		{name: "summary", kind: "summary", expected: "Zusammengefasster Ausgleich"},
		{name: "unknown kind", kind: "future-kind", expected: "Ausgleich"},
		{name: "empty kind", kind: "", expected: "Ausgleich"},
		{name: "untrusted kind", kind: "=1+1", expected: "Ausgleich"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := settlementKindLabel(tc.kind); got != tc.expected {
				t.Fatalf("settlementKindLabel(%q) = %q, want %q", tc.kind, got, tc.expected)
			}
		})
	}
}
