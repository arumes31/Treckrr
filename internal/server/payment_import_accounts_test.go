package server

import (
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

func TestIBANInvoiceAccountsSelectsAccountNotInvoice(t *testing.T) {
	targets := []store.InvoiceRefTarget{
		{ID: 1, NeighborID: 10, YearID: 2026, Number: "first", PaymentReference: "reference", NeighborName: "Synthetic"},
		{ID: 2, NeighborID: 10, YearID: 2026, Number: "second"},
		{ID: 3, NeighborID: 20, YearID: 2025},
		{ID: 4, NeighborID: 20, YearID: 2026},
		{ID: 5, NeighborID: 20, YearID: 2025},
		{ID: 6, NeighborID: 30, YearID: 2026},
	}
	got := ibanInvoiceAccounts(targets)
	if len(got) != 2 {
		t.Fatalf("accounts=%+v want two unambiguous accounts", got)
	}
	for _, neighborID := range []int64{10, 30} {
		target, ok := got[neighborID]
		if !ok || target.YearID != 2026 || target.NeighborID != neighborID ||
			target.ID != 0 || target.Number != "" || target.PaymentReference != "" {
			t.Fatalf("IBAN inferred an explicit invoice or wrong account: %+v", target)
		}
	}
	if _, ok := got[20]; ok {
		t.Fatal("multi-year account must require manual selection")
	}
	if targets[0].ID != 1 || targets[0].Number != "first" || targets[0].PaymentReference != "reference" {
		t.Fatal("matching changed reference/manual assignment targets")
	}
	if got := ibanInvoiceAccounts(nil); len(got) != 0 {
		t.Fatalf("empty targets=%+v", got)
	}
}
