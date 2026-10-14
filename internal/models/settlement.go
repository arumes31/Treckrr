package models

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// SettlementLine explains an applied reduction separately from frozen invoice
// content. Amount is positive; renderers show it as a deduction. Labels contain
// customer-safe descriptions only, never private payment or account notes.
// A zero Date explicitly means the source date is not uniquely attributable.
type SettlementLine struct {
	Date   time.Time
	Kind   string
	Label  string
	Amount decimal.Decimal
}

// SettlementLines validates a complete explanation without recalculating money.
// Nil preserves compatibility with aggregate-only callers and historical data;
// an explicitly supplied but incomplete explanation fails closed.
func SettlementLines(lines []SettlementLine, applied decimal.Decimal) ([]SettlementLine, error) {
	if applied.IsNegative() || HasSubCent(applied) {
		return nil, fmt.Errorf("invalid applied settlement")
	}
	if lines == nil && applied.IsPositive() {
		return []SettlementLine{{Kind: "summary", Label: "Zusammengefasster Ausgleich (Einzelnachweis nicht verfügbar)", Amount: applied}}, nil
	}
	total := decimal.Zero
	for _, line := range lines {
		if !line.Amount.IsPositive() || HasSubCent(line.Amount) || strings.TrimSpace(line.Label) == "" {
			return nil, fmt.Errorf("invalid settlement line")
		}
		total = total.Add(line.Amount)
	}
	if !total.Equal(applied) {
		return nil, fmt.Errorf("settlement lines do not match applied amount")
	}
	return slices.Clone(lines), nil
}
