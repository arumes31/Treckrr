//go:build integration

package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/shopspring/decimal"
)

// TestInvoiceYearCloseSettlementPreservesValuesIntegration preserves the usual
// book-close-settle workflow with one invoice and with several invoices.
func TestInvoiceYearCloseSettlementPreservesValuesIntegration(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d invoices", count), func(t *testing.T) {
			testInvoiceYearCloseSettlement(t, count)
		})
	}
}

func testInvoiceYearCloseSettlement(t *testing.T, invoiceCount int) {
	t.Helper()
	e := newItEnv(t)
	nid, yid := e.neighborID, e.yearID64
	e.book(nid, "2026-06-01")
	e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})
	invoice, err := e.st.GetInvoice(e.ctx, yid, nid)
	if err != nil || invoice.Content == nil {
		t.Fatalf("issued invoice: %v", err)
	}
	gross := invoice.Content.Gross
	if invoiceCount > 1 {
		e.book(nid, "2026-06-02")
		e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})
		invoices, err := e.st.ListActiveInvoices(e.ctx, yid, nid)
		if err != nil || len(invoices) != invoiceCount {
			t.Fatalf("subsequent invoice: count=%d error=%v", len(invoices), err)
		}
		gross = decimal.Zero
		for _, document := range invoices {
			gross = gross.Add(document.Content.Gross)
		}
	}
	partial := decimal.NewFromInt(10)
	if !gross.GreaterThan(partial) {
		t.Fatalf("fixture gross %s must exceed partial payment %s", gross, partial)
	}
	frozen := settlementPreservationSnapshot(t, e)
	e.post(fmt.Sprintf("/neighbors/%d/payments", nid), url.Values{
		"year_id": {itoa64(yid)}, "amount": {partial.StringFixed(2)},
		"paid_on": {"2026-06-02"}, "method": {"bar"},
		"idempotency_key": {e.uname + "-partial"},
	})
	payments, err := e.st.ListPayments(e.ctx, yid, nid)
	if err != nil || len(payments) != 1 || !payments[0].Amount.Equal(partial) {
		t.Fatalf("partial payment: count=%d, error=%v", len(payments), err)
	}
	var originalPayment string
	if err := e.pool.QueryRowContext(e.ctx,
		`SELECT to_jsonb(p)::text FROM payments p WHERE id=$1`, payments[0].ID,
	).Scan(&originalPayment); err != nil {
		t.Fatal(err)
	}
	beforeClose := invoiceGuardSnapshot(t, e)
	e.post(fmt.Sprintf("/years/%d/status", yid), url.Values{"status": {"completed"}})
	year, err := e.st.GetBillingYear(e.ctx, yid)
	if err != nil || !year.Completed() {
		t.Fatalf("year completion: %v", err)
	}
	if after := invoiceGuardSnapshot(t, e); after != beforeClose {
		t.Fatal("year completion changed stored financial or evidence rows")
	}
	remaining, err := e.st.AccountRemaining(e.ctx, yid, nid)
	if err != nil || !remaining.Equal(gross.Sub(partial)) {
		t.Fatalf("remaining after closing = %s, error=%v; want %s", remaining, err, gross.Sub(partial))
	}
	settle := url.Values{
		"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"origin": {"payments"}, "paid_on": {"2026-06-03"}, "method": {"bar"},
		// A stale browser amount must not override the server's current balance.
		"amount": {gross.StringFixed(2)}, "note": {"Synthetic final settlement"},
	}
	e.post("/years/mark-paid", settle)
	afterSettlement := invoiceGuardSnapshot(t, e)
	e.post("/years/mark-paid", settle)
	if after := invoiceGuardSnapshot(t, e); after != afterSettlement {
		t.Fatal("repeated mark-paid changed financial or evidence rows")
	}
	if after := settlementPreservationSnapshot(t, e); after != frozen {
		t.Fatal("payments or year completion changed an invoice, booking, ledger or evidence row")
	}
	var preservedPayment string
	if err := e.pool.QueryRowContext(e.ctx,
		`SELECT to_jsonb(p)::text FROM payments p WHERE id=$1`, payments[0].ID,
	).Scan(&preservedPayment); err != nil {
		t.Fatal(err)
	}
	if preservedPayment != originalPayment {
		t.Fatal("settlement changed the existing partial payment")
	}
	payments, err = e.st.ListPayments(e.ctx, yid, nid)
	if err != nil || len(payments) != 2 {
		t.Fatalf("settled payments: count=%d, error=%v; want two", len(payments), err)
	}
	var paid decimal.Decimal
	var foundSettlement bool
	for _, payment := range payments {
		paid = paid.Add(payment.Amount)
		if payment.Note == "Synthetic final settlement" {
			foundSettlement = payment.Amount.Equal(remaining) && payment.Method == "bar" &&
				payment.PaidOn.Format("2006-01-02") == "2026-06-03"
		}
	}
	if !paid.Equal(gross) || !foundSettlement {
		t.Fatalf("paid=%s, correct settlement=%v; want %s and true", paid, foundSettlement, gross)
	}
	if after, err := e.st.AccountRemaining(e.ctx, yid, nid); err != nil || !after.IsZero() {
		t.Fatalf("settled balance=%s, error=%v; want zero", after, err)
	}
	summaries, err := e.st.YearNeighborSummaries(e.ctx, yid)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries: count=%d, error=%v", len(summaries), err)
	}
	if summary := summaries[0]; !summary.Paid || summary.Credit || !summary.Remaining.IsZero() {
		t.Fatalf("settled summary: paid=%v, credit=%v, remaining=%s", summary.Paid, summary.Credit, summary.Remaining)
	}
	checks, err := e.st.YearClosingChecks(e.ctx, yid)
	if err != nil {
		t.Fatal(err)
	}
	foundUnpaid := false
	for _, check := range checks {
		if check.Key == "unpaid" {
			foundUnpaid = true
			if check.Count != 0 || !check.Amount.IsZero() {
				t.Fatalf("settled closing check: count=%d, amount=%s", check.Count, check.Amount)
			}
		}
	}
	if !foundUnpaid {
		t.Fatal("closing checklist omitted the unpaid check")
	}
	e.book(nid, "2026-07-01")
	if after := invoiceGuardSnapshot(t, e); after != afterSettlement {
		t.Fatal("closed year accepted new work or changed existing financial rows")
	}
}

// settlementPreservationSnapshot excludes only payments, which may be appended
// during settlement. All other full rows must remain byte-for-byte equivalent.
func settlementPreservationSnapshot(t *testing.T, e *itEnv) string {
	t.Helper()
	var rows map[string]json.RawMessage
	if err := json.Unmarshal([]byte(invoiceGuardSnapshot(t, e)), &rows); err != nil {
		t.Fatal(err)
	}
	delete(rows, "payments")
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
