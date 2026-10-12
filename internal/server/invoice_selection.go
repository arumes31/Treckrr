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

// requestInvoice is preparation only; no handler uses it yet. It resolves an
// explicitly selected immutable invoice within the route account. The legacy
// branch remains safe only while the current single-active-invoice constraint
// holds; multi-invoice activation also requires GetInvoice to reject ambiguity.
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
