package server

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

func documentViewFixture() models.Invoice {
	date := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	return models.Invoice{
		ID: 41, BillingYearID: 7, NeighborID: 9, Number: "2026-0041",
		IssuedOn: date, Created: date, Kind: "invoice", Status: "issued", PaymentReference: "RF18539007547034",
		Content: &models.InvoiceContent{
			Net: decimal.RequireFromString("100.01"), VATRate: decimal.NewFromInt(13),
			VATAmount: decimal.NewFromInt(13), Gross: decimal.RequireFromString("113.01"),
			ShowVAT: true, TaxMode: "pauschal", TaxNote: "Frozen tax note",
			ServiceFrom: date, ServiceTo: date, PaymentTermDays: 14,
			Issuer:    models.InvoiceParty{Name: "Frozen issuer", Address: "Frozen address", IBAN: "AT611904300234573201"},
			Recipient: models.InvoiceParty{Name: "Frozen recipient", Address: "Frozen recipient address"},
			Lines: []models.InvoiceLine{{Date: date, Label: "First invoice only", Unit: "h",
				Quantity: decimal.NewFromInt(2), UnitPrice: decimal.NewFromInt(50), Cost: decimal.RequireFromString("100.01")}},
			SkontoPct: decimal.NewFromInt(2), SkontoUntil: date.AddDate(0, 0, 10), Hash: "stored-content-hash",
		},
	}
}

// TestInvoiceDocumentViewPreservesFrozenValues includes a historical line whose
// saved cost differs from multiplication, so a hidden recalculation is detected.
func TestInvoiceDocumentViewPreservesFrozenValues(t *testing.T) {
	t.Parallel()
	invoice := documentViewFixture()
	view, err := newInvoiceDocumentView(invoice, decimal.RequireFromString("83.01"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Invoice, invoice) {
		t.Fatal("view altered frozen invoice substance")
	}
	if !view.Remaining.Equal(decimal.RequireFromString("83.01")) ||
		!view.AppliedSettlement.Equal(decimal.NewFromInt(30)) || !view.HasEpcQR || !view.Active {
		t.Fatalf("invalid document settlement projection: %+v", view)
	}
	if view.PaymentReference != invoice.PaymentReference {
		t.Fatal("payment reference changed")
	}
	view.Invoice.Content.Lines[0].Label = "View-only adaptation"
	view.Invoice.Content.Issuer.Name = "View-only issuer"
	if invoice.Content.Lines[0].Label != "First invoice only" || invoice.Content.Issuer.Name != "Frozen issuer" {
		t.Fatal("view aliases the caller's mutable snapshot")
	}
}

// TestInvoiceDocumentViewIsolation proves rendering a second invoice cannot
// change the already-built first view or its stored figures.
func TestInvoiceDocumentViewIsolation(t *testing.T) {
	t.Parallel()
	first := documentViewFixture()
	firstView, err := newInvoiceDocumentView(first, first.Content.Gross)
	if err != nil {
		t.Fatal(err)
	}
	second := documentViewFixture()
	second.ID, second.Number = 42, "2026-0042"
	second.Content.Lines[0].Label = "Second invoice only"
	second.Content.Gross = decimal.NewFromInt(200)
	if _, err := newInvoiceDocumentView(second, decimal.NewFromInt(150)); err != nil {
		t.Fatal(err)
	}
	if firstView.Invoice.ID != 41 || firstView.Invoice.Content.Lines[0].Label != "First invoice only" ||
		!firstView.Invoice.Content.Gross.Equal(decimal.RequireFromString("113.01")) ||
		!firstView.Remaining.Equal(first.Content.Gross) {
		t.Fatal("second document contaminated the first")
	}
}

func TestInvoiceDocumentViewStates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		change    func(*models.Invoice)
		remaining string
		invalid   bool
		qr        bool
	}{
		{name: "unpaid", remaining: "113.01", qr: true},
		{name: "paid", remaining: "0"},
		{name: "no frozen IBAN", change: func(iv *models.Invoice) { iv.Content.Issuer.IBAN = "" }, remaining: "113.01"},
		{name: "canceled history", change: func(iv *models.Invoice) { iv.Status = "canceled" }, remaining: "0"},
		{name: "canceled cannot collect", change: func(iv *models.Invoice) { iv.Status = "canceled" }, remaining: "1", invalid: true},
		{name: "negative historical gross", change: func(iv *models.Invoice) { iv.Content.Gross = decimal.NewFromInt(-10) }, remaining: "0"},
		{name: "negative remaining", remaining: "-0.01", invalid: true},
		{name: "annual charges not document claim", remaining: "114", invalid: true},
		{name: "missing snapshot", change: func(iv *models.Invoice) { iv.Content = nil }, remaining: "0", invalid: true},
		{name: "wrong document kind", change: func(iv *models.Invoice) { iv.Kind = "gutschrift" }, remaining: "0", invalid: true},
		{name: "unknown status", change: func(iv *models.Invoice) { iv.Status = "draft" }, remaining: "0", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invoice := documentViewFixture()
			if tc.change != nil {
				tc.change(&invoice)
			}
			view, err := newInvoiceDocumentView(invoice, decimal.RequireFromString(tc.remaining))
			if errors.Is(err, errInvalidInvoiceDocument) != tc.invalid || (err != nil) != tc.invalid {
				t.Fatalf("error = %v, invalid=%v", err, tc.invalid)
			}
			if !tc.invalid && view.HasEpcQR != tc.qr {
				t.Errorf("QR visible=%v, want %v", view.HasEpcQR, tc.qr)
			}
		})
	}
}
