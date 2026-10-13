package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/web"
)

func TestInvoiceDocumentTemplateUsesOnlyFrozenValues(t *testing.T) {
	t.Parallel()
	invoice := documentViewFixture()
	view, err := newInvoiceDocumentView(invoice, decimal.NewFromInt(83))
	if err != nil {
		t.Fatal(err)
	}
	templates, err := web.Templates()
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{
		"Doc": view, "Invoice": invoice, "Year": &models.BillingYear{ID: 7, Year: 2026},
		"Neighbor": &models.Neighbor{ID: 9, Name: "LIVE NEIGHBOR MUST NOT LEAK"},
		"Shared":   true, "PortalToken": "synthetic-token",
		"Ledger": []models.LedgerEntry{{Description: "ANNUAL PRIVATE NOTE"}},
	}
	var rendered bytes.Buffer
	if err := templates["invoice_document"].ExecuteTemplate(&rendered, "content", data); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	for _, want := range []string{"Frozen issuer", "Frozen recipient", "First invoice only", "100,01", "113,01", "83,00", "/s/portal/synthetic-token/invoice.pdf", "/s/portal/synthetic-token/epc-qr.png"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing frozen document value %q", want)
		}
	}
	for _, forbidden := range []string{"LIVE NEIGHBOR", "ANNUAL PRIVATE NOTE", "/neighbors/9/", "/invoice/storno", "/beleg/email"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("shared invoice exposed %q", forbidden)
		}
	}
}

func TestAttachedInvoiceCreditsStayDocumentScoped(t *testing.T) {
	t.Parallel()
	invoice := documentViewFixture()
	otherID := invoice.ID + 1
	credit := models.Invoice{ID: 99, BillingYearID: invoice.BillingYearID, NeighborID: invoice.NeighborID,
		Kind: "gutschrift", Status: "issued", ReferencesInvoiceID: &invoice.ID, Number: "SELECTED-CREDIT"}
	other := credit
	other.ReferencesInvoiceID, other.Number = &otherID, "OTHER-CREDIT"
	standalone := credit
	standalone.ReferencesInvoiceID = nil
	canceled := credit
	canceled.Status = "canceled"
	wrongYear := credit
	wrongYear.BillingYearID++
	wrongNeighbor := credit
	wrongNeighbor.NeighborID++
	wrongKind := credit
	wrongKind.Kind = "storno"
	got := attachedInvoiceCredits(invoice, []models.Invoice{other, standalone, canceled, wrongYear, wrongNeighbor, wrongKind, credit})
	if len(got) != 1 || got[0].Number != credit.Number {
		t.Fatalf("attached credits include another document or inactive/foreign records: %+v", got)
	}
}
