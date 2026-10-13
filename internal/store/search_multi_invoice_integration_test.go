//go:build integration

package store_test

import (
	"fmt"
	"testing"

	"github.com/d0linger/treckrr/internal/models"
)

func TestSearchKeepsExactInvoiceIdentity(t *testing.T) {
	st, _, yearID, neighborID, _ := invoiceFixture(t, false)
	ctx := t.Context()
	first := issueFixtureInvoice(t, st, yearID, neighborID)
	if _, err := st.CreateEntry(ctx, flatEntry(yearID, neighborID, 2026, "50"), nil); err != nil {
		t.Fatal(err)
	}
	second := issueFixtureInvoice(t, st, yearID, neighborID)
	if _, err := st.CreateEntry(ctx, flatEntry(yearID, neighborID, 2026, "25"), nil); err != nil {
		t.Fatal(err)
	}
	assertTarget := func(t *testing.T, document models.Invoice, wantURL string) {
		t.Helper()
		results, err := st.Search(ctx, document.Number, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if result.Kind == "invoice" && result.Label == "Rechnung "+document.Number {
				if result.URL != wantURL {
					t.Fatalf("document %s target = %q, want %q", document.Number, result.URL, wantURL)
				}
				return
			}
		}
		t.Fatalf("document %s missing from search results", document.Number)
	}
	for _, invoice := range []models.Invoice{first, second} {
		t.Run(invoice.Number, func(t *testing.T) {
			assertTarget(t, invoice, fmt.Sprintf("/neighbors/%d/beleg?year=%d&invoice_id=%d", neighborID, yearID, invoice.ID))
		})
	}
	reversal, err := st.StornoInvoiceByID(ctx, yearID, neighborID, first.ID, "Synthetic search history")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("canceled invoice still selects its own snapshot", func(t *testing.T) {
		assertTarget(t, first, fmt.Sprintf("/neighbors/%d/beleg?year=%d&invoice_id=%d", neighborID, yearID, first.ID))
	})
	t.Run("other document kinds retain their supported account route", func(t *testing.T) {
		assertTarget(t, reversal, fmt.Sprintf("/neighbors/%d/beleg?year=%d", neighborID, yearID))
	})
}
