package store

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// This pure planner never writes financial data. Runtime adapters supply one
// consistent account snapshot; annual balances additionally include pending
// source entries, which are deliberately outside the document allocation.

var errInvalidAllocationInput = errors.New("invalid invoice allocation input")

type allocationInvoice struct {
	ID       int64
	IssuedOn time.Time
	Gross    decimal.Decimal
	// AttachedCredit is the signed sum of this invoice's active credit notes.
	AttachedCredit decimal.Decimal
}

type allocationPayment struct {
	ID     int64
	PaidOn time.Time
	Amount decimal.Decimal
	// PreferredInvoiceID is a validated active invoice in this account, not
	// the legacy historical attribution. Zero requests ordinary FIFO.
	PreferredInvoiceID int64
	ReversalOfID       int64
}

type invoiceAllocationInput struct {
	Invoices []allocationInvoice
	Payments []allocationPayment
	// Ledger is the current signed account ledger net. StandaloneCredits is
	// the non-positive sum of active credit notes without a reference invoice.
	Ledger            decimal.Decimal
	StandaloneCredits decimal.Decimal
}

type plannedPaymentAllocation struct {
	PaymentID int64
	InvoiceID int64
	Amount    decimal.Decimal
}

type invoiceAllocationPlan struct {
	Remaining       map[int64]decimal.Decimal
	OffsetApplied   map[int64]decimal.Decimal
	Payments        []plannedPaymentAllocation
	UnallocatedCash decimal.Decimal
	UnusedOffset    decimal.Decimal
	// AccountResidual is outside the document balances: positive ledger net,
	// less unused offsets and cash. Pending entry net is not an input here.
	AccountResidual decimal.Decimal
}

// planInvoiceAllocation distributes account offsets once, then real payments
// once, in calendar-date/id order. A payment's selected invoice has priority;
// its excess proceeds FIFO and any surplus remains account credit. Reversals
// must fully reverse exactly one positive payment, matching the bank-import
// contract. The caller supplies only one account's active documents/payments
// and must validate ownership before constructing these inputs.
//
// No input is mutated, no amount is silently rounded, and an invalid input
// returns no partial plan. This function neither changes invoices nor creates
// cash. Runtime readers and invariant tests use the same allocation contract.
func planInvoiceAllocation(input invoiceAllocationInput) (invoiceAllocationPlan, error) {
	if !wholeAllocationCents(input.Ledger) || !wholeAllocationCents(input.StandaloneCredits) || input.StandaloneCredits.IsPositive() {
		return invoiceAllocationPlan{}, fmt.Errorf("%w: ledger or standalone credit", errInvalidAllocationInput)
	}
	invoices := slices.Clone(input.Invoices)
	slices.SortFunc(invoices, func(a, b allocationInvoice) int {
		return compareAllocationOrder(a.IssuedOn, a.ID, b.IssuedOn, b.ID)
	})
	plan := invoiceAllocationPlan{
		Remaining:     make(map[int64]decimal.Decimal, len(invoices)),
		OffsetApplied: make(map[int64]decimal.Decimal, len(invoices)),
	}
	offset := decimal.Max(input.Ledger.Neg(), decimal.Zero).Sub(input.StandaloneCredits)
	for _, invoice := range invoices {
		_, duplicate := plan.Remaining[invoice.ID]
		if invoice.ID <= 0 || invoice.IssuedOn.IsZero() || duplicate ||
			!wholeAllocationCents(invoice.Gross) || !wholeAllocationCents(invoice.AttachedCredit) ||
			invoice.AttachedCredit.IsPositive() ||
			invoice.AttachedCredit.Neg().GreaterThan(decimal.Max(invoice.Gross, decimal.Zero)) {
			return invoiceAllocationPlan{}, fmt.Errorf("%w: invoice %d", errInvalidAllocationInput, invoice.ID)
		}
		claim := invoice.Gross.Add(invoice.AttachedCredit)
		plan.Remaining[invoice.ID] = decimal.Max(claim, decimal.Zero)
		// A historical negative invoice is an account offset, never a negative
		// open document. Its signed gross is still counted exactly once.
		offset = offset.Add(decimal.Max(claim.Neg(), decimal.Zero))
	}
	payments, err := allocationNetPayments(input.Payments, plan.Remaining)
	if err != nil {
		return invoiceAllocationPlan{}, err
	}
	for _, invoice := range invoices {
		applied := decimal.Min(offset, plan.Remaining[invoice.ID])
		plan.OffsetApplied[invoice.ID] = applied
		plan.Remaining[invoice.ID] = plan.Remaining[invoice.ID].Sub(applied)
		offset = offset.Sub(applied)
	}
	plan.UnusedOffset = offset
	for _, payment := range payments {
		available := payment.Amount
		apply := func(invoiceID int64) {
			amount := decimal.Min(available, plan.Remaining[invoiceID])
			if !amount.IsPositive() {
				return
			}
			plan.Payments = append(plan.Payments, plannedPaymentAllocation{
				PaymentID: payment.ID, InvoiceID: invoiceID, Amount: amount,
			})
			plan.Remaining[invoiceID] = plan.Remaining[invoiceID].Sub(amount)
			available = available.Sub(amount)
		}
		if payment.PreferredInvoiceID != 0 {
			apply(payment.PreferredInvoiceID)
		}
		for _, invoice := range invoices {
			if available.IsZero() {
				break
			}
			apply(invoice.ID)
		}
		plan.UnallocatedCash = plan.UnallocatedCash.Add(available)
	}
	plan.AccountResidual = decimal.Max(input.Ledger, decimal.Zero).Sub(plan.UnusedOffset).Sub(plan.UnallocatedCash)
	return plan, nil
}

// allocationNetPayments validates exact reversal pairs before allocating, so
// reversing a targeted payment cannot consume a different payment's allocation.
func allocationNetPayments(payments []allocationPayment, invoices map[int64]decimal.Decimal) ([]allocationPayment, error) {
	byID := make(map[int64]allocationPayment, len(payments))
	for _, payment := range payments {
		_, duplicate := byID[payment.ID]
		if payment.ID <= 0 || payment.PaidOn.IsZero() || duplicate || !wholeAllocationCents(payment.Amount) ||
			payment.Amount.IsZero() || payment.ReversalOfID < 0 || payment.PreferredInvoiceID < 0 ||
			(payment.ReversalOfID == 0 && payment.Amount.IsNegative()) ||
			(payment.ReversalOfID != 0 && !payment.Amount.IsNegative()) {
			return nil, fmt.Errorf("%w: payment %d", errInvalidAllocationInput, payment.ID)
		}
		if payment.PreferredInvoiceID != 0 {
			if _, ok := invoices[payment.PreferredInvoiceID]; !ok {
				return nil, fmt.Errorf("%w: payment %d target", errInvalidAllocationInput, payment.ID)
			}
		}
		byID[payment.ID] = payment
	}
	reversed := make(map[int64]bool)
	for _, payment := range payments {
		if payment.ReversalOfID == 0 {
			continue
		}
		original, ok := byID[payment.ReversalOfID]
		if !ok || original.ReversalOfID != 0 || reversed[original.ID] || !original.Amount.Equal(payment.Amount.Neg()) {
			return nil, fmt.Errorf("%w: reversal %d", errInvalidAllocationInput, payment.ID)
		}
		reversed[original.ID] = true
	}
	net := make([]allocationPayment, 0, len(payments))
	for _, payment := range payments {
		if payment.ReversalOfID == 0 && !reversed[payment.ID] {
			net = append(net, payment)
		}
	}
	slices.SortFunc(net, func(a, b allocationPayment) int {
		return compareAllocationOrder(a.PaidOn, a.ID, b.PaidOn, b.ID)
	})
	return net, nil
}

func wholeAllocationCents(amount decimal.Decimal) bool {
	return amount.Equal(amount.Round(2))
}

// compareAllocationOrder compares stored DATE values, not timezone offsets.
func compareAllocationOrder(a time.Time, aID int64, b time.Time, bID int64) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	if order := cmp.Compare(ay, by); order != 0 {
		return order
	}
	if order := cmp.Compare(am, bm); order != 0 {
		return order
	}
	if order := cmp.Compare(ad, bd); order != 0 {
		return order
	}
	return cmp.Compare(aID, bID)
}
