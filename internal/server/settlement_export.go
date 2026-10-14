package server

import (
	"fmt"
	"net/http"

	"github.com/d0linger/treckrr/internal/web"
)

// handleSettlementCSV exports current applied reductions separately from the
// frozen journal. Invoice rows carry gross/open once; detail rows carry signed
// deductions only, so summing a numeric column cannot duplicate invoice totals.
func (s *Server) handleSettlementCSV(w http.ResponseWriter, r *http.Request) {
	neighborID, err := pathID(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	year, ok := s.resolveYear(w, r)
	if !ok {
		return
	}
	settlement, err := s.store.ReadAccountSettlement(r.Context(), year.ID, neighborID)
	if err != nil {
		s.invoiceRequestError(w, r, err)
		return
	}
	selectedID, selected, err := requestedInvoiceID(r)
	if err != nil {
		s.invoiceRequestError(w, r, err)
		return
	}
	found := !selected
	for _, invoice := range settlement.Allocations.Invoices {
		found = found || invoice.InvoiceID == selectedID
	}
	if !found {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cw, finish := csvDownload(w, r, fmt.Sprintf("zahlungszuordnung-%d-%d.csv", year.Year, neighborID))
	defer finish()
	if err := cw.Write([]string{"Datensatz", "Rechnung", "Datum", "Bezeichnung", "Rechnungsbetrag (EUR)", "Angerechnet (EUR)", "Offen (EUR)"}); err != nil {
		return
	}
	for _, invoice := range settlement.Allocations.Invoices {
		if selected && invoice.InvoiceID != selectedID {
			continue
		}
		number := csvSafe(settlement.InvoiceNumbers[invoice.InvoiceID])
		if err := cw.Write([]string{"Rechnung", number, "", "Festgeschriebener Rechnungsbetrag", deDecimal(invoice.Gross), "", deDecimal(invoice.Remaining)}); err != nil {
			return
		}
		for _, line := range settlement.Lines[invoice.InvoiceID] {
			date := "nicht eindeutig"
			if !line.Date.IsZero() {
				date = web.Date(line.Date)
			}
			if err := cw.Write([]string{
				settlementKindLabel(line.Kind), number, date,
				csvSafe(line.Label), "", deDecimal(line.Amount.Neg()), "",
			}); err != nil {
				return
			}
		}
	}
}

func settlementKindLabel(kind string) string {
	switch kind {
	case "payment":
		return "Zahlung"
	case "credit":
		return "Gutschrift"
	case "offset":
		return "Verrechnung"
	case "summary":
		return "Zusammengefasster Ausgleich"
	default:
		return "Ausgleich"
	}
}
