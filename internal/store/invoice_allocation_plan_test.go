package store

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func allocationTestInvoice(id int64, gross string) allocationInvoice {
	return allocationInvoice{ID: id, IssuedOn: time.Date(2026, 1, int(id), 0, 0, 0, 0, time.UTC), Gross: decimal.RequireFromString(gross)}
}

func allocationTestPayment(id int64, amount string, preferred int64) allocationPayment {
	return allocationPayment{
		ID: id, PaidOn: time.Date(2026, 2, int(id), 0, 0, 0, 0, time.UTC),
		Amount: decimal.RequireFromString(amount), PreferredInvoiceID: preferred,
	}
}

func TestPlanInvoiceAllocation(t *testing.T) {
	tests := []struct {
		name      string
		input     invoiceAllocationInput
		remaining map[int64]string
		cash      string
		offset    string
		residual  string
	}{
		{name: "empty", cash: "0", offset: "0", residual: "0"},
		{
			name: "oldest open first",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(2, "200"), allocationTestInvoice(1, "100")},
				Payments: []allocationPayment{allocationTestPayment(1, "150", 0)},
			},
			remaining: map[int64]string{1: "0", 2: "150"}, cash: "0", offset: "0", residual: "0",
		},
		{
			name: "explicit later invoice first",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")},
				Payments: []allocationPayment{allocationTestPayment(1, "50", 2)},
			},
			remaining: map[int64]string{1: "100", 2: "150"}, cash: "0", offset: "0", residual: "0",
		},
		{
			name: "explicit excess proceeds fifo",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")},
				Payments: []allocationPayment{allocationTestPayment(1, "250", 2)},
			},
			remaining: map[int64]string{1: "50", 2: "0"}, cash: "0", offset: "0", residual: "0",
		},
		{
			name: "surplus is account credit",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")},
				Payments: []allocationPayment{allocationTestPayment(1, "345.67", 0)},
			},
			remaining: map[int64]string{1: "0", 2: "0"}, cash: "45.67", offset: "0", residual: "-45.67",
		},
		{
			name:  "advance before invoice",
			input: invoiceAllocationInput{Payments: []allocationPayment{allocationTestPayment(1, "123.45", 0)}},
			cash:  "123.45", offset: "0", residual: "-123.45",
		},
		{
			name: "positive ledger stays outside documents",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100")},
				Payments: []allocationPayment{allocationTestPayment(1, "125", 0)}, Ledger: decimal.NewFromInt(40),
			},
			remaining: map[int64]string{1: "0"}, cash: "25", offset: "0", residual: "15",
		},
		{
			name: "net ledger and standalone credit used once",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")},
				Payments: []allocationPayment{allocationTestPayment(1, "70", 0)},
				Ledger:   decimal.NewFromInt(-30), StandaloneCredits: decimal.NewFromInt(-20),
			},
			remaining: map[int64]string{1: "0", 2: "180"}, cash: "0", offset: "0", residual: "0",
		},
		{
			name: "surplus offsets are not cash",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100")},
				Payments: []allocationPayment{allocationTestPayment(1, "50", 0)}, Ledger: decimal.NewFromInt(-130),
			},
			remaining: map[int64]string{1: "0"}, cash: "50", offset: "30", residual: "-80",
		},
		{
			name: "historical negative invoice preserves signed balance",
			input: invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "-40"), allocationTestInvoice(2, "100")},
			},
			remaining: map[int64]string{1: "0", 2: "60"}, cash: "0", offset: "0", residual: "0",
		},
		{
			name:      "zero invoice remains zero",
			input:     invoiceAllocationInput{Invoices: []allocationInvoice{allocationTestInvoice(1, "0")}},
			remaining: map[int64]string{1: "0"}, cash: "0", offset: "0", residual: "0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planInvoiceAllocation(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Remaining) != len(test.remaining) {
				t.Fatalf("remaining count = %d, want %d", len(plan.Remaining), len(test.remaining))
			}
			for id, want := range test.remaining {
				assertAllocationMoney(t, plan.Remaining[id], want)
			}
			assertAllocationMoney(t, plan.UnallocatedCash, test.cash)
			assertAllocationMoney(t, plan.UnusedOffset, test.offset)
			assertAllocationMoney(t, plan.AccountResidual, test.residual)
			assertAllocationInvariants(t, test.input, plan)
		})
	}
}

func TestPlanInvoiceAllocationAttachedCreditIsDocumentScoped(t *testing.T) {
	first, second := allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")
	second.AttachedCredit = decimal.NewFromInt(-200)
	input := invoiceAllocationInput{Invoices: []allocationInvoice{second, first}}
	plan, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	assertAllocationMoney(t, plan.Remaining[1], "100")
	assertAllocationMoney(t, plan.Remaining[2], "0")
	assertAllocationMoney(t, plan.OffsetApplied[1], "0")
	assertAllocationInvariants(t, input, plan)
}

func TestPlanInvoiceAllocationReversalDoesNotConsumeUnrelatedPayment(t *testing.T) {
	reversal := allocationTestPayment(3, "-100", 0)
	reversal.ReversalOfID = 1
	input := invoiceAllocationInput{
		Invoices: []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")},
		Payments: []allocationPayment{reversal, allocationTestPayment(2, "50", 2), allocationTestPayment(1, "100", 1)},
	}
	plan, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	assertAllocationMoney(t, plan.Remaining[1], "100")
	assertAllocationMoney(t, plan.Remaining[2], "150")
	if len(plan.Payments) != 1 || plan.Payments[0].PaymentID != 2 || plan.Payments[0].InvoiceID != 2 {
		t.Fatalf("unexpected allocations: %+v", plan.Payments)
	}
	assertAllocationInvariants(t, input, plan)
}

func TestPlanInvoiceAllocationCalendarDateAndIDOrder(t *testing.T) {
	first, second := allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")
	first.IssuedOn = time.Date(2026, 3, 29, 23, 0, 0, 0, time.FixedZone("west", -12*3600))
	second.IssuedOn = time.Date(2026, 3, 29, 1, 0, 0, 0, time.FixedZone("east", 14*3600))
	p1, p2 := allocationTestPayment(1, "150", 2), allocationTestPayment(2, "150", 2)
	p1.PaidOn, p2.PaidOn = first.IssuedOn, second.IssuedOn
	input := invoiceAllocationInput{Invoices: []allocationInvoice{second, first}, Payments: []allocationPayment{p2, p1}}
	plan, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Payments) != 3 || plan.Payments[0].PaymentID != 1 || plan.Payments[0].InvoiceID != 2 ||
		plan.Payments[1].PaymentID != 2 || plan.Payments[1].InvoiceID != 2 || plan.Payments[2].InvoiceID != 1 {
		t.Fatalf("unexpected calendar/id order: %+v", plan.Payments)
	}
	assertAllocationInvariants(t, input, plan)
}

func TestPlanInvoiceAllocationReplansAdvanceAndLedgerPayout(t *testing.T) {
	input := invoiceAllocationInput{Payments: []allocationPayment{allocationTestPayment(1, "150", 0)}}
	advance, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	assertAllocationMoney(t, advance.AccountResidual, "-150")
	input.Invoices = []allocationInvoice{allocationTestInvoice(1, "100"), allocationTestInvoice(2, "200")}
	input.Ledger = decimal.NewFromInt(-70)
	before, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	assertAllocationMoney(t, before.Remaining[2], "80")
	input.Ledger = decimal.NewFromInt(10) // Negative offset plus a positive payout posting, netted once.
	after, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	assertAllocationMoney(t, after.Remaining[2], "150")
	assertAllocationMoney(t, after.AccountResidual, "10")
	assertAllocationInvariants(t, input, after)
}

func TestPlanInvoiceAllocationRejectsInvalidInputWithoutPartialPlan(t *testing.T) {
	tests := []struct {
		name   string
		change func(*invoiceAllocationInput)
	}{
		{"duplicate invoice", func(in *invoiceAllocationInput) { in.Invoices = append(in.Invoices, in.Invoices[0]) }},
		{"missing invoice id", func(in *invoiceAllocationInput) { in.Invoices[0].ID = 0 }},
		{"missing invoice date", func(in *invoiceAllocationInput) { in.Invoices[0].IssuedOn = time.Time{} }},
		{"subcent invoice", func(in *invoiceAllocationInput) { in.Invoices[0].Gross = decimal.RequireFromString("1.001") }},
		{"positive attached credit", func(in *invoiceAllocationInput) { in.Invoices[0].AttachedCredit = decimal.NewFromInt(1) }},
		{"excess attached credit", func(in *invoiceAllocationInput) { in.Invoices[0].AttachedCredit = decimal.NewFromInt(-101) }},
		{"credit on negative invoice", func(in *invoiceAllocationInput) {
			in.Invoices[0].Gross, in.Invoices[0].AttachedCredit = decimal.NewFromInt(-100), decimal.NewFromInt(-1)
		}},
		{"positive standalone credit", func(in *invoiceAllocationInput) { in.StandaloneCredits = decimal.NewFromInt(1) }},
		{"subcent ledger", func(in *invoiceAllocationInput) { in.Ledger = decimal.RequireFromString("-1.001") }},
		{"subcent credit", func(in *invoiceAllocationInput) { in.StandaloneCredits = decimal.RequireFromString("-0.001") }},
		{"duplicate payment", func(in *invoiceAllocationInput) { in.Payments = append(in.Payments, in.Payments[0]) }},
		{"missing payment id", func(in *invoiceAllocationInput) { in.Payments[0].ID = 0 }},
		{"missing payment date", func(in *invoiceAllocationInput) { in.Payments[0].PaidOn = time.Time{} }},
		{"zero payment", func(in *invoiceAllocationInput) { in.Payments[0].Amount = decimal.Zero }},
		{"subcent payment", func(in *invoiceAllocationInput) { in.Payments[0].Amount = decimal.RequireFromString("1.001") }},
		{"negative ordinary payment", func(in *invoiceAllocationInput) { in.Payments[0].Amount = decimal.NewFromInt(-50) }},
		{"unknown selected invoice", func(in *invoiceAllocationInput) { in.Payments[0].PreferredInvoiceID = 99 }},
		{"negative selected invoice", func(in *invoiceAllocationInput) { in.Payments[0].PreferredInvoiceID = -1 }},
		{"negative reversal reference", func(in *invoiceAllocationInput) { in.Payments[0].ReversalOfID = -1 }},
		{"positive reversal", func(in *invoiceAllocationInput) { in.Payments[0].ReversalOfID = 2 }},
		{"orphan reversal", func(in *invoiceAllocationInput) {
			in.Payments[0].Amount, in.Payments[0].ReversalOfID = decimal.NewFromInt(-50), 99
		}},
		{"self reversal", func(in *invoiceAllocationInput) {
			in.Payments[0].Amount, in.Payments[0].ReversalOfID = decimal.NewFromInt(-50), 1
		}},
		{"partial reversal", func(in *invoiceAllocationInput) {
			r := allocationTestPayment(2, "-49", 0)
			r.ReversalOfID = 1
			in.Payments = append(in.Payments, r)
		}},
		{"excess reversal", func(in *invoiceAllocationInput) {
			r := allocationTestPayment(2, "-51", 0)
			r.ReversalOfID = 1
			in.Payments = append(in.Payments, r)
		}},
		{"duplicate reversal", func(in *invoiceAllocationInput) {
			r1, r2 := allocationTestPayment(2, "-50", 0), allocationTestPayment(3, "-50", 0)
			r1.ReversalOfID, r2.ReversalOfID = 1, 1
			in.Payments = append(in.Payments, r1, r2)
		}},
		{"reversal of reversal", func(in *invoiceAllocationInput) {
			r1, r2 := allocationTestPayment(2, "-50", 0), allocationTestPayment(3, "-50", 0)
			r1.ReversalOfID, r2.ReversalOfID = 1, 2
			in.Payments = append(in.Payments, r1, r2)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := invoiceAllocationInput{
				Invoices: []allocationInvoice{allocationTestInvoice(1, "100")},
				Payments: []allocationPayment{allocationTestPayment(1, "50", 0)},
			}
			test.change(&input)
			plan, err := planInvoiceAllocation(input)
			if !errors.Is(err, errInvalidAllocationInput) || !reflect.DeepEqual(plan, invoiceAllocationPlan{}) {
				t.Fatalf("got plan=%+v, err=%v; want empty plan and validation error", plan, err)
			}
		})
	}
}

// assertAllocationInvariants checks independent accounting identities as well
// as attribution bounds; repeating only expected document totals would miss
// duplicated cash or a credit pool incorrectly attributed as a real payment.
func assertAllocationInvariants(t *testing.T, input invoiceAllocationInput, plan invoiceAllocationPlan) {
	t.Helper()
	expected := input.Ledger.Add(input.StandaloneCredits)
	actual := plan.AccountResidual
	claims := make(map[int64]decimal.Decimal)
	for _, invoice := range input.Invoices {
		expected = expected.Add(invoice.Gross).Add(invoice.AttachedCredit)
		claims[invoice.ID] = decimal.Max(invoice.Gross.Add(invoice.AttachedCredit), decimal.Zero)
		remaining := plan.Remaining[invoice.ID]
		if remaining.IsNegative() || !wholeAllocationCents(remaining) || remaining.GreaterThan(claims[invoice.ID]) {
			t.Fatalf("invalid document remainder: %d = %s", invoice.ID, remaining)
		}
		actual = actual.Add(remaining)
	}
	cash := decimal.Zero
	paymentAmounts := make(map[int64]decimal.Decimal)
	for _, payment := range input.Payments {
		expected = expected.Sub(payment.Amount)
		cash = cash.Add(payment.Amount)
		paymentAmounts[payment.ID] = payment.Amount
	}
	for _, payment := range input.Payments {
		if payment.ReversalOfID != 0 {
			paymentAmounts[payment.ReversalOfID] = decimal.Zero
		}
	}
	if !actual.Equal(expected) {
		t.Fatalf("balance not conserved: remainder+residual=%s, signed input balance=%s", actual, expected)
	}
	byPayment, byInvoice := map[int64]decimal.Decimal{}, map[int64]decimal.Decimal{}
	pairs := map[[2]int64]bool{}
	allocated := plan.UnallocatedCash
	for _, allocation := range plan.Payments {
		pair := [2]int64{allocation.PaymentID, allocation.InvoiceID}
		_, hasInvoice := claims[allocation.InvoiceID]
		if pairs[pair] || !hasInvoice || !allocation.Amount.IsPositive() || !wholeAllocationCents(allocation.Amount) {
			t.Fatalf("invalid allocation: %+v", allocation)
		}
		pairs[pair] = true
		allocated = allocated.Add(allocation.Amount)
		byPayment[allocation.PaymentID] = byPayment[allocation.PaymentID].Add(allocation.Amount)
		byInvoice[allocation.InvoiceID] = byInvoice[allocation.InvoiceID].Add(allocation.Amount)
	}
	if !allocated.Equal(cash) || plan.UnallocatedCash.IsNegative() || plan.UnusedOffset.IsNegative() {
		t.Fatalf("cash not conserved: allocated+surplus=%s, net cash=%s", allocated, cash)
	}
	for id, amount := range byPayment {
		if amount.GreaterThan(paymentAmounts[id]) {
			t.Fatalf("payment %d overallocated: %s > %s", id, amount, paymentAmounts[id])
		}
	}
	for id, claim := range claims {
		if !plan.Remaining[id].Add(byInvoice[id]).Add(plan.OffsetApplied[id]).Equal(claim) {
			t.Fatalf("invoice %d not conserved", id)
		}
	}
}

func assertAllocationMoney(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.RequireFromString(want)) {
		t.Fatalf("amount = %s, want %s", got, want)
	}
}

func TestPlanInvoiceAllocationProperties(t *testing.T) {
	for seed := range 256 {
		data := make([]byte, seed%65)
		for i := range data {
			data[i] = byte((seed + i*37) % 256)
		}
		checkAllocationProperties(t, data)
	}
}

func FuzzPlanInvoiceAllocationConservesMoney(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3, 4, 5, 6})
	f.Add([]byte{255, 0, 128, 9, 70, 2, 80, 4})
	f.Fuzz(checkAllocationProperties)
}

func checkAllocationProperties(t *testing.T, data []byte) {
	t.Helper()
	// Bound work per input; values are generated as integer cents so every
	// property trial describes a valid, exactly representable account.
	if len(data) > 64 {
		data = data[:64]
	}
	input := invoiceAllocationInput{}
	for i, value := range data {
		id := int64(i + 1)
		invoice := allocationTestInvoice(id, "0")
		invoice.Gross = decimal.New(int64(value)*97, -2)
		invoice.AttachedCredit = decimal.New(-int64(value%7), -2)
		input.Invoices = append(input.Invoices, invoice)
		payment := allocationTestPayment(id, "1", 0)
		payment.Amount = decimal.New(int64(value)*103+1, -2)
		if value%3 == 0 {
			payment.PreferredInvoiceID = id
		}
		input.Payments = append(input.Payments, payment)
		if value%5 == 0 {
			reversal := payment
			reversal.ID, reversal.ReversalOfID, reversal.Amount = id+100, id, payment.Amount.Neg()
			input.Payments = append(input.Payments, reversal)
		}
		input.Ledger = input.Ledger.Add(decimal.New(int64(value)-127, -2))
		input.StandaloneCredits = input.StandaloneCredits.Sub(decimal.New(int64(value%11), -2))
	}
	originalInvoices, originalPayments := slices.Clone(input.Invoices), slices.Clone(input.Payments)
	plan, err := planInvoiceAllocation(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Invoices, originalInvoices) || !reflect.DeepEqual(input.Payments, originalPayments) {
		t.Fatal("planner mutated input")
	}
	assertAllocationInvariants(t, input, plan)
	slices.Reverse(input.Invoices)
	slices.Reverse(input.Payments)
	reordered, err := planInvoiceAllocation(input)
	if err != nil || !reflect.DeepEqual(plan, reordered) {
		t.Fatalf("input order changed plan: err=%v", err)
	}
}
