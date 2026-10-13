//go:build integration

package store_test

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestInvoicePaymentTermRoundTripIntegration(t *testing.T) {
	for _, days := range []int{0, 14} {
		t.Run(strconv.Itoa(days), func(t *testing.T) {
			st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
			if _, err := pool.ExecContext(t.Context(), `UPDATE company SET payment_term_days=$1`, days); err != nil {
				t.Fatal(err)
			}
			invoice := issueFixtureInvoice(t, st, yearID, neighborID)
			if invoice.Content.PaymentTermDays != days || !invoice.Content.PaymentTermFrozen {
				t.Fatal("issued term was lost")
			}
			before, _ := json.Marshal(invoice.Content)
			if _, err := pool.ExecContext(t.Context(), `UPDATE company SET payment_term_days=90`); err != nil {
				t.Fatal(err)
			}
			reloaded, err := st.GetInvoiceDocument(t.Context(), invoice.ID)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(reloaded.Content)
			if string(before) != string(after) || invoice.Content.Hash != reloaded.Content.Hash || !reloaded.Content.PaymentTermFrozen {
				t.Fatal("payment terms or frozen content changed on SQL roundtrip")
			}
			rows, err := st.DunningRows(t.Context(), yearID, 90, day(2026, 10, 1))
			if err != nil || len(rows) != 1 || rows[0].TermDays != days || !rows[0].DueOn.Equal(invoice.IssuedOn.AddDate(0, 0, days)) {
				t.Fatalf("frozen dunning term changed with company settings: %+v, %v", rows, err)
			}
		})
	}
}
