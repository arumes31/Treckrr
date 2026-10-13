package server

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

var errInvalidInvoiceDocument = errors.New("invalid invoice document")

// invoiceDocumentView deliberately contains no live annual bookings, catalog
// data, ledger rows or payment rows. Document consumers use this projection;
// annual account consumers retain their separate, live settlement view.
type invoiceDocumentView struct {
	Invoice           models.Invoice
	Remaining         decimal.Decimal
	AppliedSettlement decimal.Decimal
	PaymentReference  string
	Active            bool
	HasEpcQR          bool
}

// newInvoiceDocumentView copies frozen substance without recalculating prices,
// VAT, dates, parties or hashes. Remaining is supplied by the authoritative
// document-allocation service, never derived from the annual account here.
// AppliedSettlement includes credits and offsets, not just cash payments.
func newInvoiceDocumentView(invoice models.Invoice, remaining decimal.Decimal) (invoiceDocumentView, error) {
	if invoice.ID <= 0 || invoice.BillingYearID <= 0 || invoice.NeighborID <= 0 ||
		invoice.Kind != "invoice" || invoice.Content == nil ||
		(invoice.Status != "issued" && invoice.Status != "canceled") {
		return invoiceDocumentView{}, fmt.Errorf("%w: missing frozen invoice", errInvalidInvoiceDocument)
	}
	active := invoice.Status == "issued"
	claim := decimal.Max(invoice.Content.Gross, decimal.Zero)
	if remaining.IsNegative() || remaining.GreaterThan(claim) || (!active && !remaining.IsZero()) {
		return invoiceDocumentView{}, fmt.Errorf("%w: invalid document remaining", errInvalidInvoiceDocument)
	}
	// Copy mutable containers so adapting this view cannot change the caller's
	// in-memory snapshot. decimal.Decimal values themselves are immutable.
	content := *invoice.Content
	content.Lines = slices.Clone(content.Lines)
	invoice.Content = &content
	if invoice.ReferencesInvoiceID != nil {
		id := *invoice.ReferencesInvoiceID
		invoice.ReferencesInvoiceID = &id
	}
	reference := invoice.PaymentReference
	if reference == "" {
		reference = invoice.Number
	}
	view := invoiceDocumentView{
		Invoice: invoice, Remaining: remaining, Active: active,
		PaymentReference: reference,
		HasEpcQR:         active && remaining.IsPositive() && strings.TrimSpace(content.Issuer.IBAN) != "",
	}
	if active {
		view.AppliedSettlement = claim.Sub(remaining)
	}
	return view, nil
}
