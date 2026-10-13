package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

func TestInvoiceSourceMismatch(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	entries := []models.Entry{
		{
			Date: date, Note: "Frozen note", Unit: "h",
			Hours: decimal.RequireFromString("1.005"), HourlyRate: decimal.RequireFromString("12.345"),
			Quantity: decimal.NewFromInt(999), UnitPrice: decimal.NewFromInt(999),
			Cost: decimal.RequireFromString("12.41"),
		},
		{
			Date: date, TaskLabel: "Frozen quantity", Unit: "Ballen",
			Quantity: decimal.NewFromInt(2), UnitPrice: decimal.RequireFromString("8.125"),
			Cost: decimal.RequireFromString("16.25"),
		},
	}
	snapshot := invoiceSourceSnapshot{
		net:   decimal.NullDecimal{Decimal: decimal.RequireFromString("28.66"), Valid: true},
		gross: decimal.NullDecimal{Decimal: decimal.RequireFromString("34.39"), Valid: true},
		hash:  "frozen-source-only-check",
		lines: []byte(`[
			{"date":"2026-10-10T00:00:00Z","label":"Frozen note","unit":"h","qty":"1.005","unit_price":"12.345","cost":"12.41"},
			{"date":"2026-10-10T00:00:00Z","label":"Frozen quantity","unit":"Ballen","qty":2,"unit_price":8.125,"cost":16.25}
		]`),
	}
	for _, tc := range []struct {
		name   string
		change func(*invoiceSourceSnapshot, *[]models.Entry)
		want   string
	}{
		{name: "exact hourly and quantity sources"},
		{name: "missing net", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.net.Valid = false }, want: "missing_snapshot"},
		{name: "missing gross", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.gross.Valid = false }, want: "missing_snapshot"},
		{name: "missing hash", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.hash = "" }, want: "missing_snapshot"},
		{name: "missing lines", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.lines = nil }, want: "missing_snapshot"},
		{name: "malformed lines", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.lines = []byte(`[{`) }, want: "invalid_snapshot"},
		{name: "null lines", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.lines = []byte(`null`) }, want: "invalid_snapshot"},
		{name: "object lines", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.lines = []byte(`{}`) }, want: "invalid_snapshot"},
		{name: "null line item", change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) { s.lines = []byte(`[null]`) }, want: "invalid_snapshot"},
		{
			name: "empty snapshot with no sources",
			change: func(s *invoiceSourceSnapshot, e *[]models.Entry) {
				s.lines, s.net.Decimal, *e = []byte(`[]`), decimal.Zero, nil
			},
			want: "invalid_snapshot",
		},
		{
			name: "extra current source",
			change: func(_ *invoiceSourceSnapshot, e *[]models.Entry) {
				*e = append(*e, (*e)[0])
			},
			want: "source_count_mismatch",
		},
		{
			name: "voided current source",
			change: func(_ *invoiceSourceSnapshot, e *[]models.Entry) {
				(*e)[1].Voided = true
			},
			want: "source_count_mismatch",
		},
		{
			name: "different source order",
			change: func(_ *invoiceSourceSnapshot, e *[]models.Entry) {
				(*e)[0], (*e)[1] = (*e)[1], (*e)[0]
			},
			want: "source_line_mismatch",
		},
		{
			name: "changed frozen net",
			change: func(s *invoiceSourceSnapshot, _ *[]models.Entry) {
				s.net.Decimal = decimal.NewFromInt(29)
			},
			want: "source_total_mismatch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input, sources := snapshot, append([]models.Entry{}, entries...)
			if tc.change != nil {
				tc.change(&input, &sources)
			}
			if got := invoiceSourceMismatch(input, sources); got != tc.want {
				t.Fatalf("mismatch = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInvoiceSourceMismatchRequiresCompleteLines(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	entry := models.Entry{Date: date, TaskLabel: "Frozen zero", Hours: decimal.NewFromInt(2)}
	line := models.InvoiceLine{Date: date, Label: entry.TaskLabel, Quantity: entry.Hours}
	for _, field := range []string{"date", "label", "unit", "qty", "unit_price", "cost"} {
		for _, form := range []string{"missing", "null"} {
			t.Run(field+" "+form, func(t *testing.T) {
				t.Parallel()
				raw, err := json.Marshal(line)
				if err != nil {
					t.Fatal(err)
				}
				var object map[string]json.RawMessage
				if err := json.Unmarshal(raw, &object); err != nil {
					t.Fatal(err)
				}
				delete(object, field)
				if form == "null" {
					object[field] = json.RawMessage(`null`)
				}
				raw, err = json.Marshal([]map[string]json.RawMessage{object})
				if err != nil {
					t.Fatal(err)
				}
				snapshot := invoiceSourceSnapshot{
					net: decimal.NullDecimal{Valid: true}, gross: decimal.NullDecimal{Valid: true},
					hash: "frozen-source-only-check", lines: raw,
				}
				if got := invoiceSourceMismatch(snapshot, []models.Entry{entry}); got != "invalid_snapshot" {
					t.Fatalf("incomplete %s field: mismatch=%q, want invalid_snapshot", field, got)
				}
			})
		}
	}
	t.Run("explicit zeroes and blank legacy unit remain valid", func(t *testing.T) {
		raw, err := json.Marshal([]models.InvoiceLine{line})
		if err != nil {
			t.Fatal(err)
		}
		snapshot := invoiceSourceSnapshot{
			net: decimal.NullDecimal{Valid: true}, gross: decimal.NullDecimal{Valid: true},
			hash: "frozen-source-only-check", lines: raw,
		}
		if got := invoiceSourceMismatch(snapshot, []models.Entry{entry}); got != "" {
			t.Fatalf("explicit zero source: mismatch=%q", got)
		}
	})
	t.Run("zero date cannot identify historical service", func(t *testing.T) {
		invalidLine, invalidEntry := line, entry
		invalidLine.Date, invalidEntry.Date = time.Time{}, time.Time{}
		raw, err := json.Marshal([]models.InvoiceLine{invalidLine})
		if err != nil {
			t.Fatal(err)
		}
		snapshot := invoiceSourceSnapshot{
			net: decimal.NullDecimal{Valid: true}, gross: decimal.NullDecimal{Valid: true},
			hash: "frozen-source-only-check", lines: raw,
		}
		if got := invoiceSourceMismatch(snapshot, []models.Entry{invalidEntry}); got != "invalid_snapshot" {
			t.Fatalf("zero-date source: mismatch=%q, want invalid_snapshot", got)
		}
	})
}
