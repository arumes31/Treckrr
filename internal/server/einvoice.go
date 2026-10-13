package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/d0linger/treckrr/internal/einvoice"
)

// handleEInvoiceXML downloads one ebInterface 6.1 document. It accepts only an
// immutable issued invoice snapshot; legacy/live rebuilding is forbidden.
func (s *Server) handleEInvoiceXML(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	iv, err := s.store.GetInvoiceDocument(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if missing := einvoice.MissingFields(iv); len(missing) > 0 {
		s.badRequest(w, "E-Rechnung nicht bereit: "+strings.Join(einvoice.MissingFields(iv), ", ")+".")
		return
	}
	view, err := s.loadInvoiceDocumentView(r.Context(), iv)
	if err != nil {
		s.invoiceRequestError(w, r, err)
		return
	}
	company, err := s.store.GetCompany(r.Context())
	if err != nil {
		s.serverError(w, r.URL.Path, err)
		return
	}
	neighbor, err := s.store.GetNeighbor(r.Context(), iv.NeighborID)
	if err != nil {
		s.invoiceRequestError(w, r, err)
		return
	}
	b, err := einvoice.RenderWithPaymentStatus(iv, einvoice.PaymentStatus{
		Remaining: view.Remaining, AsOf: view.SettlementAsOf,
		DueOn: iv.IssuedOn.AddDate(0, 0, invoicePaymentTerm(iv, company, *neighbor)),
	})
	if err != nil {
		s.serverError(w, r.URL.Path, err)
		return
	}
	name := zipNameSafe.ReplaceAllString(iv.Number, "_") + "-ebinterface-6p1.xml"
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}
