package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

var errInvalidInvoiceDocument = errors.New("invalid invoice document")

// invoiceDocumentView contains frozen substance and customer-safe applied
// settlement rows only, never private notes or unrelated annual account rows.
type invoiceDocumentView struct {
	Invoice           models.Invoice
	Remaining         decimal.Decimal
	AppliedSettlement decimal.Decimal
	SettlementAsOf    time.Time
	PaymentReference  string
	Active            bool
	HasEpcQR          bool
	SettlementLines   []models.SettlementLine
}

// loadInvoiceDocumentView adds a dated, read-only settlement projection after
// the caller has authorized access to this invoice. It never changes its snapshot.
func (s *Server) loadInvoiceDocumentView(ctx context.Context, invoice models.Invoice) (invoiceDocumentView, error) {
	asOf := time.Now()
	remaining := decimal.Zero
	var lines []models.SettlementLine
	if invoice.Status == "issued" {
		account, err := s.store.ReadAccountSettlement(ctx, invoice.BillingYearID, invoice.NeighborID)
		if err != nil {
			return invoiceDocumentView{}, fmt.Errorf("invoice settlement: %w", err)
		}
		found := false
		for _, allocation := range account.Allocations.Invoices {
			if allocation.InvoiceID == invoice.ID {
				remaining, lines, found = allocation.Remaining, account.Lines[invoice.ID], true
				break
			}
		}
		if !found {
			return invoiceDocumentView{}, store.ErrNotFound
		}
	}
	view, err := newInvoiceDocumentView(invoice, remaining)
	if err != nil {
		return invoiceDocumentView{}, err
	}
	view.SettlementAsOf = asOf
	return view.withSettlementLines(lines)
}

// withSettlementLines attaches the invoice's settlement explanation after
// checking it accounts exactly for AppliedSettlement.
func (v invoiceDocumentView) withSettlementLines(lines []models.SettlementLine) (invoiceDocumentView, error) {
	var err error
	v.SettlementLines, err = models.SettlementLines(lines, v.AppliedSettlement)
	if err != nil {
		return invoiceDocumentView{}, fmt.Errorf("%w: %w", errInvalidInvoiceDocument, err)
	}
	return v, nil
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
	return view.withSettlementLines(nil)
}
