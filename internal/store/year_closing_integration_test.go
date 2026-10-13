//go:build integration

package store_test

import (
	"reflect"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/store"
)

func TestYearClosingAccountRemaindersIntegration(t *testing.T) {
	cases := []struct {
		name, ledger, payment, pending, annual, documents, residual string
		invoiceCount, unpaidCount                                   int
		noBookings                                                  bool
	}{
		{name: "paid invoice leaves ledger debt", invoiceCount: 1, ledger: "20", payment: "110",
			annual: "10", documents: "0", residual: "10"},
		{name: "invoice and ledger debt stay separate", invoiceCount: 1, ledger: "20",
			annual: "120", documents: "100", residual: "20", unpaidCount: 1},
		{name: "two invoices count residual account only once", invoiceCount: 2, ledger: "20",
			annual: "320", documents: "300", residual: "20", unpaidCount: 2},
		{name: "pending work remains outside invoice debt", invoiceCount: 2, payment: "150", pending: "50",
			annual: "200", documents: "150", residual: "50", unpaidCount: 1},
		{name: "unbilled only", annual: "100", documents: "0", residual: "100"},
		{name: "ledger only", noBookings: true, ledger: "20", annual: "20", documents: "0", residual: "20"},
		{name: "zero balance", invoiceCount: 1, payment: "100", annual: "0", documents: "0", residual: "0"},
		{name: "credit balance", invoiceCount: 1, payment: "150", annual: "-50", documents: "0", residual: "0"},
		{name: "negative ledger already offsets invoices", invoiceCount: 1, ledger: "-25",
			annual: "75", documents: "75", residual: "0", unpaidCount: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			st, pool, yearID, neighborID, entryID := invoiceFixture(t, false)
			if tc.noBookings {
				if err := st.DeleteEntry(ctx, entryID); err != nil {
					t.Fatal(err)
				}
			}
			for i := range tc.invoiceCount {
				if i > 0 {
					addMultiAllocationWork(t, st, yearID, neighborID, "200")
				}
				issueFixtureInvoice(t, st, yearID, neighborID)
			}
			if tc.pending != "" {
				addMultiAllocationWork(t, st, yearID, neighborID, tc.pending)
			}
			if tc.ledger != "" {
				_, err := st.AddNeighborLedger(
					ctx, yearID, neighborID, dec(tc.ledger), "Synthetic closing ledger", day(2026, 9, 11),
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.payment != "" {
				if err := st.AddPayment(
					ctx, yearID, neighborID, dec(tc.payment), day(2026, 9, 11), "Synthetic closing payment", "bank",
				); err != nil {
					t.Fatal(err)
				}
			}
			before := invoiceSourceState(t, pool)
			checks, err := st.YearClosingChecks(ctx, yearID)
			if err != nil {
				t.Fatal(err)
			}
			if after := invoiceSourceState(t, pool); !reflect.DeepEqual(before, after) {
				t.Fatal("closing checklist changed financial or audit rows")
			}
			if annual, err := st.AccountRemaining(ctx, yearID, neighborID); err != nil || !annual.Equal(dec(tc.annual)) {
				t.Fatalf("annual balance=%s err=%v, want %s", annual, err, tc.annual)
			}
			documents := closingCheckByKey(t, checks, "unpaid")
			if documents.Count != tc.unpaidCount || !documents.Amount.Equal(dec(tc.documents)) {
				t.Fatalf("document warning=%+v, want %d/%s", documents, tc.unpaidCount, tc.documents)
			}
			residual := closingCheckByKey(t, checks, "account_unpaid")
			wantCount := 0
			if dec(tc.residual).IsPositive() {
				wantCount = 1
			}
			if residual.Count != wantCount || !residual.Amount.Equal(dec(tc.residual)) {
				t.Fatalf("account warning=%+v, want %d/%s", residual, wantCount, tc.residual)
			}
			if len(residual.Neighbors) != wantCount || len(residual.Names) != wantCount {
				t.Fatalf("residual account must be listed once: %+v", residual)
			}
			if wantCount == 1 && residual.Neighbors[0].ID != neighborID {
				t.Fatalf("wrong residual account: %+v", residual.Neighbors)
			}
		})
	}
}

func TestYearClosingAccountRemaindersCloseAndSettleIntegration(t *testing.T) {
	ctx := t.Context()
	f := newMultiAllocationFixture(t)
	if _, err := f.store.AddNeighborLedger(
		ctx, f.yearID, f.neighborID, dec("20"), "Synthetic closing debit", day(2026, 9, 11),
	); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddPayment(
		ctx, f.yearID, f.neighborID, dec("310"), day(2026, 9, 11), "Synthetic closing payment", "bank",
	); err != nil {
		t.Fatal(err)
	}
	// Another account's credit must not cancel the first account's warning.
	creditNeighborID, err := f.store.CreateNeighbor(ctx, "Synthetic closing credit neighbor", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddNeighborToYear(ctx, f.yearID, creditNeighborID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddPayment(
		ctx, f.yearID, creditNeighborID, dec("50"), day(2026, 9, 11), "Synthetic advance", "bank",
	); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"in_progress", "completed"} {
		if err := f.store.SetYearStatus(ctx, f.yearID, status); err != nil {
			t.Fatal(err)
		}
		checks, err := f.store.YearClosingChecks(ctx, f.yearID)
		if err != nil {
			t.Fatal(err)
		}
		residual := closingCheckByKey(t, checks, "account_unpaid")
		if residual.Count != 1 || !residual.Amount.Equal(dec("10")) {
			t.Fatalf("%s residual warning=%+v, want 1/10", status, residual)
		}
	}
	before := invoiceSourceState(t, f.pool)
	paid, err := f.store.SettleRemaining(ctx, f.yearID, f.neighborID, day(2026, 9, 12), "Synthetic closing settlement")
	if err != nil || !paid.Equal(dec("10")) {
		t.Fatalf("closed-year settlement=%s err=%v, want 10", paid, err)
	}
	checks, err := f.store.YearClosingChecks(ctx, f.yearID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"unpaid", "account_unpaid"} {
		check := closingCheckByKey(t, checks, key)
		if !check.Clean() || !check.Amount.IsZero() {
			t.Fatalf("settled %s warning=%+v", key, check)
		}
	}
	if credit, err := f.store.AccountRemaining(ctx, f.yearID, creditNeighborID); err != nil || !credit.Equal(dec("-50")) {
		t.Fatalf("unrelated account credit changed: %s err=%v", credit, err)
	}
	after := invoiceSourceState(t, f.pool)
	for _, table := range []string{"invoices", "entries", "neighbor_ledger"} {
		if after[table] != before[table] {
			t.Fatalf("settlement changed existing %s", table)
		}
	}
	paid, err = f.store.SettleRemaining(ctx, f.yearID, f.neighborID, day(2026, 9, 12), "Synthetic settlement replay")
	if err != nil || !paid.Equal(decimal.Zero) {
		t.Fatalf("settlement replay=%s err=%v, want 0", paid, err)
	}
}

func closingCheckByKey(t *testing.T, checks []store.ClosingCheck, key string) store.ClosingCheck {
	t.Helper()
	for _, check := range checks {
		if check.Key == key {
			return check
		}
	}
	t.Fatalf("missing closing check %q", key)
	return store.ClosingCheck{}
}
