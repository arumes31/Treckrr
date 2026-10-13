package web

import (
	"strings"
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

func TestLegacyInventoryInvoiceSourceCheckRenders(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check *store.InvoiceSourceCheck
		want  string
	}{
		{name: "not requested", want: "Die zusätzliche Prüfung startet erst"},
		{name: "empty", check: &store.InvoiceSourceCheck{}, want: "Keine aktiven Rechnungen"},
		{name: "matched", check: &store.InvoiceSourceCheck{Checked: 1, Matched: 1, MatchedEntries: 2}, want: "keine Freigabe einer Datenmigration"},
		{name: "mismatch", check: &store.InvoiceSourceCheck{Checked: 1, Issues: []store.InvoiceSourceIssue{{InvoiceID: 42, YearID: 7, NeighborID: 3, Reason: "source_line_mismatch"}}}, want: "Vor Migration prüfen &lt;unverändert&gt;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := execPage(t, "legacy_inventory", map[string]any{
				"Inventory":          store.LegacyInventory{},
				"InvoiceSourceCheck": tc.check,
				"InvoiceSourceIssues": []map[string]any{{
					"InvoiceID": 42, "YearID": 7, "NeighborID": 3,
					"Description": "Vor Migration prüfen <unverändert>",
				}},
			})
			for _, want := range []string{tc.want, "Rechnungszuordnung prüfen", "schaltet mehrere Rechnungen pro Jahr nicht frei"} {
				if !strings.Contains(page, want) {
					t.Errorf("missing %q", want)
				}
			}
			if tc.check == nil && strings.Contains(page, "Geprüfte Rechnungen") {
				t.Fatal("ordinary inventory displayed an invoice source result")
			}
		})
	}
}
