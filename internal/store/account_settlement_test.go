package store

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestExplainAccountSettlement covers exact, ambiguous and summarized sources:
// payments, counterservices, credits, historical surpluses and reversals.
func TestExplainAccountSettlement(t *testing.T) {
	t.Parallel()
	d := decimal.RequireFromString
	date := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		input    invoiceAllocationInput
		sources  []settlementSource
		payments []settlementPayment
		rows     int
		unknown  bool
	}{
		{name: "one invoice two exact payments and counterservice", input: invoiceAllocationInput{
			Invoices: []allocationInvoice{allocationTestInvoice(1, "322.20")}, Ledger: d("-44"),
			Payments: []allocationPayment{allocationTestPayment(1, "60", 0), allocationTestPayment(2, "40", 0)},
		}, sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("322.20")}, {id: 9, kind: "ledger", date: date, amount: d("-44")}},
			payments: []settlementPayment{{id: 1, date: date, amount: d("60"), method: "bar"}, {id: 2, date: date, amount: d("40"), method: "bank"}}, rows: 3},
		{name: "mixed ledger stays a net source", input: invoiceAllocationInput{Invoices: []allocationInvoice{allocationTestInvoice(1, "100")}, Ledger: d("-60")},
			sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}, {id: 8, kind: "ledger", date: date, amount: d("-100")}, {id: 9, kind: "ledger", date: date, amount: d("40")}}, rows: 1, unknown: true},
		{name: "partial use of multiple sources is ambiguous", input: invoiceAllocationInput{Invoices: []allocationInvoice{allocationTestInvoice(1, "20"), allocationTestInvoice(2, "100")}, Ledger: d("-44")},
			sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("20")}, {id: 2, kind: "invoice", number: "R2", amount: d("100")}, {id: 8, kind: "ledger", date: date, amount: d("-30")}, {id: 9, kind: "ledger", date: date, amount: d("-14")}}, rows: 1, unknown: true},
		{name: "attached credit is exact", input: invoiceAllocationInput{Invoices: []allocationInvoice{{ID: 1, IssuedOn: date, Gross: d("100"), AttachedCredit: d("-15")}}},
			sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}, {id: 2, referenceID: 1, kind: "gutschrift", number: "R1-G1", date: date, amount: d("-15")}}, rows: 1},
		{name: "standalone credit is the only source", input: invoiceAllocationInput{Invoices: []allocationInvoice{allocationTestInvoice(1, "100")}, StandaloneCredits: d("-15")},
			sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}, {id: 2, kind: "gutschrift", number: "G1", date: date, amount: d("-15")}}, rows: 1},
		{name: "negative historical invoice supplies an exact offset", input: invoiceAllocationInput{Invoices: []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "-15")}},
			sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}, {id: 2, kind: "invoice", number: "R2", date: date, amount: d("-15")}}, rows: 1},
		{name: "positive ledger does not invent an applied counterservice", input: invoiceAllocationInput{Invoices: []allocationInvoice{allocationTestInvoice(1, "100")}, Ledger: d("1")},
			sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}, {id: 8, kind: "ledger", amount: d("-44")}, {id: 9, kind: "ledger", amount: d("45")}}, rows: 0},
		{name: "later reversal preserves unrelated payment", input: invoiceAllocationInput{
			Invoices: []allocationInvoice{allocationTestInvoice(1, "100")}, Payments: []allocationPayment{
				{ID: 1, PaidOn: date.AddDate(0, 0, -20), Amount: d("100")},
				{ID: 2, PaidOn: date, Amount: d("-100"), ReversalOfID: 1},
				{ID: 3, PaidOn: date.AddDate(0, 0, -10), Amount: d("25")},
			},
		}, sources: []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}}, payments: []settlementPayment{
			{id: 1, date: date.AddDate(0, 0, -20), amount: d("100")},
			{id: 2, date: date, amount: d("-100"), reversalID: 1},
			{id: 3, date: date.AddDate(0, 0, -10), amount: d("25")},
		}, rows: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planInvoiceAllocation(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			summary := InvoiceAllocationSummary{UnallocatedCash: plan.UnallocatedCash, UnusedOffset: plan.UnusedOffset, AccountResidual: plan.AccountResidual}
			for _, invoice := range tc.input.Invoices {
				a := InvoiceAllocation{InvoiceID: invoice.ID, Gross: invoice.Gross, AttachedCredit: invoice.AttachedCredit, Remaining: plan.Remaining[invoice.ID], OffsetApplied: plan.OffsetApplied[invoice.ID]}
				for _, p := range plan.Payments {
					if p.InvoiceID == invoice.ID {
						a.Paid = a.Paid.Add(p.Amount)
						a.PaymentParts = append(a.PaymentParts, InvoicePaymentPart{PaymentID: p.PaymentID, Amount: p.Amount})
					}
				}
				summary.Invoices = append(summary.Invoices, a)
			}
			got, err := explainAccountSettlement(summary, tc.sources, tc.payments)
			if err != nil {
				t.Fatal(err)
			}
			lines := got.Lines[1]
			if len(lines) != tc.rows {
				t.Fatalf("lines=%+v, want %d", lines, tc.rows)
			}
			if tc.unknown && (!lines[0].Date.IsZero() || !strings.Contains(lines[0].Label, "Verrechnung aus Jahreskonto")) {
				t.Fatalf("invented provenance: %+v", lines)
			}
			for _, a := range got.Allocations.Invoices {
				if !a.Remaining.Equal(plan.Remaining[a.InvoiceID]) {
					t.Fatal("display changed allocation")
				}
			}
			if tc.name == "later reversal preserves unrelated payment" {
				for _, paymentID := range []int64{1, 2} {
					usage := got.Payments[paymentID]
					if !usage.Reversed || len(usage.Parts) != 0 || !usage.Unallocated.IsZero() {
						t.Fatalf("reversed payment %d still has usage: %+v", paymentID, usage)
					}
				}
				usage := got.Payments[3]
				if usage.Reversed || !usage.Unallocated.IsZero() || len(usage.Parts) != 1 ||
					usage.Parts[0].InvoiceID != 1 || usage.Parts[0].InvoiceNumber != "R1" || !usage.Parts[0].Amount.Equal(d("25")) {
					t.Fatalf("unrelated payment allocation changed: %+v", usage)
				}
				if lines[0].Kind != "payment" || !lines[0].Amount.Equal(d("25")) || !got.OpenInvoices.Equal(d("75")) {
					t.Fatalf("reversal changed settlement: lines=%+v open=%s", lines, got.OpenInvoices)
				}
			}
		})
	}
}

// TestExplainAccountSettlementPaymentSplitAndSurplus splits one payment across
// invoices, keeps its surplus unallocated, keeps free-text methods out of
// labels, and fails when payment metadata is missing.
func TestExplainAccountSettlementPaymentSplitAndSurplus(t *testing.T) {
	t.Parallel()
	d := decimal.RequireFromString
	summary := InvoiceAllocationSummary{Invoices: []InvoiceAllocation{
		{InvoiceID: 1, Gross: d("100"), Paid: d("100"), PaymentParts: []InvoicePaymentPart{{PaymentID: 9, Amount: d("100")}}},
		{InvoiceID: 2, Gross: d("200"), Paid: d("200"), PaymentParts: []InvoicePaymentPart{{PaymentID: 9, Amount: d("200")}}},
	}}
	sources := []settlementSource{{id: 1, kind: "invoice", number: "R1", amount: d("100")}, {id: 2, kind: "invoice", number: "R2", amount: d("200")}}
	got, err := explainAccountSettlement(summary, sources, []settlementPayment{{id: 9, date: time.Now(), amount: d("350"), method: "untrusted private text"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Payments[9].Parts) != 2 || !got.Payments[9].Unallocated.Equal(d("50")) {
		t.Fatalf("usage=%+v", got.Payments[9])
	}
	if got.Lines[1][0].Label != "Zahlung" || !got.Lines[1][0].Amount.Equal(d("100")) {
		t.Fatalf("wrong or private row: %+v", got.Lines[1])
	}
	summary.Invoices[0].PaymentParts[0].PaymentID = 999
	if _, err := explainAccountSettlement(summary, sources, nil); err == nil {
		t.Fatal("missing metadata accepted")
	}
}
