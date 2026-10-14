//go:build integration

package store_test

import (
	"reflect"
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

func TestReadAccountSettlementPreservesValuesIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	if _, err := f.store.AddNeighborLedger(t.Context(), f.yearID, f.neighborID, dec("-44"), "PRIVATE LEDGER NOTE", day(2026, 9, 11)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GutschriftInvoiceByID(t.Context(), f.yearID, f.neighborID, f.second.ID, dec("10"), "PRIVATE CREDIT NOTE"); err != nil {
		t.Fatal(err)
	}
	payment, err := f.store.RecordPayment(t.Context(), store.PaymentInput{YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID, Amount: dec("350"), PaidOn: day(2026, 9, 12), Method: "bar", Note: "PRIVATE PAYMENT NOTE"})
	if err != nil {
		t.Fatal(err)
	}
	before := invoiceSourceState(t, f.pool)
	old, err := f.store.ReadInvoiceAccountReconciliation(t.Context(), f.yearID, f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.store.ReadAccountSettlement(t.Context(), f.yearID, f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, detail.InvoiceAccountReconciliation) {
		t.Fatalf("reconciliation changed: %+v / %+v", old, detail.InvoiceAccountReconciliation)
	}
	usage := detail.Payments[payment.PaymentID]
	if len(usage.Parts) != 2 || !usage.Unallocated.Equal(dec("104")) {
		t.Fatalf("split/surplus=%+v", usage)
	}
	if len(detail.Lines[f.first.ID]) != 2 || len(detail.Lines[f.second.ID]) != 2 {
		t.Fatalf("rows=%+v", detail.Lines)
	}
	for _, allocation := range detail.Allocations.Invoices {
		total := dec("0")
		for _, line := range detail.Lines[allocation.InvoiceID] {
			total = total.Add(line.Amount)
		}
		if !total.Add(allocation.Remaining).Equal(allocation.Gross) {
			t.Fatalf("invoice %d does not reconcile", allocation.InvoiceID)
		}
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(before, after) {
		t.Fatal("read changed financial/audit rows")
	}
}
