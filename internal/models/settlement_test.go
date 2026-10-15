package models

import (
	"testing"

	"github.com/shopspring/decimal"
)

// TestSettlementLines covers complete, legacy-aggregate and invalid
// explanations, and that the result does not alias the input slice.
func TestSettlementLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		lines   []SettlementLine
		applied string
		invalid bool
	}{
		{name: "unpaid", lines: []SettlementLine{}, applied: "0"},
		{name: "legacy aggregate", applied: "35"},
		{name: "complete", lines: []SettlementLine{{Label: "Zahlung", Amount: decimal.NewFromInt(20)}, {Label: "Gutschrift", Amount: decimal.NewFromInt(15)}}, applied: "35"},
		{name: "incomplete", lines: []SettlementLine{}, applied: "35", invalid: true},
		{name: "wrong total", lines: []SettlementLine{{Label: "Zahlung", Amount: decimal.NewFromInt(30)}}, applied: "35", invalid: true},
		{name: "negative", applied: "-1", invalid: true},
		{name: "subcent", applied: "1.001", invalid: true},
		{name: "zero row", lines: []SettlementLine{{Label: "Zahlung"}}, applied: "0", invalid: true},
		{name: "missing label", lines: []SettlementLine{{Amount: decimal.NewFromInt(1)}}, applied: "1", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SettlementLines(tc.lines, decimal.RequireFromString(tc.applied))
			if (err != nil) != tc.invalid {
				t.Fatalf("lines=%+v, err=%v", got, err)
			}
			if err == nil && len(got) > 0 {
				got[0].Label = "copy only"
				if len(tc.lines) > 0 && tc.lines[0].Label == "copy only" {
					t.Fatal("input slice was aliased")
				}
			}
		})
	}
}
