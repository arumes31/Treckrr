package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

var errInvoiceSelection = errors.New("invalid invoice selection")

// requestedInvoiceID distinguishes absent selection from malformed or duplicated
// input. A malformed ID must never silently fall back to another document.
func requestedInvoiceID(r *http.Request) (int64, bool, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return 0, false, errInvoiceSelection
	}
	values, present := r.Form["invoice_id"]
	if !present {
		return 0, false, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, true, errInvoiceSelection
	}
	for _, c := range values[0] {
		if c < '0' || c > '9' {
			return 0, true, errInvoiceSelection
		}
	}
	id, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, true, errInvoiceSelection
	}
	return id, true, nil
}

// requestInvoice resolves a frozen invoice within the route account. Legacy
// callers may omit selection only while exactly one active invoice exists.
func (s *Server) requestInvoice(r *http.Request, yearID, neighborID int64, active bool) (models.Invoice, error) {
	id, selected, err := requestedInvoiceID(r)
	if err != nil {
		return models.Invoice{}, err
	}
	var invoice models.Invoice
	if selected {
		invoice, err = s.store.GetInvoiceDocument(r.Context(), id)
	} else {
		invoice, err = s.store.GetInvoice(r.Context(), yearID, neighborID)
	}
	if err != nil {
		return models.Invoice{}, err
	}
	validAccount := invoice.BillingYearID == yearID && invoice.NeighborID == neighborID
	validDocument := invoice.Kind == "invoice" && invoice.Content != nil
	if !validAccount || !validDocument || (active && invoice.Status != "issued") {
		return models.Invoice{}, store.ErrNotFound
	}
	return invoice, nil
}

func (s *Server) invoiceRequestError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errInvoiceSelection), errors.Is(err, store.ErrInvoiceAmbiguous):
		s.badRequest(w, "Bitte eine eindeutige Rechnung auswählen.")
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	default:
		s.serverError(w, "invoice selection", err)
	}
}

func invoiceDocumentURL(invoice models.Invoice) string {
	return "/neighbors/" + itoa64(invoice.NeighborID) + "/beleg?year=" +
		itoa64(invoice.BillingYearID) + "&invoice_id=" + itoa64(invoice.ID)
}

// optionalPaymentInvoiceID treats only absent/one empty value as FIFO.
func optionalPaymentInvoiceID(r *http.Request) (int64, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return 0, errInvoiceSelection
	}
	values, present := r.Form["invoice_id"]
	if !present || (len(values) == 1 && values[0] == "") {
		return 0, nil
	}
	id, _, err := requestedInvoiceID(r)
	return id, err
}
