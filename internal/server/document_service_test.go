package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/pdf"
)

func TestDocumentServiceBuildsInvoiceMailFromFrozenSnapshot(t *testing.T) {
	invoice := models.Invoice{
		ID: 17, Number: "2026-017", IssuedOn: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		BillingYearID: 42, NeighborID: 9, Kind: "invoice", Status: "issued",
		Content: &models.InvoiceContent{
			Net: decimal.RequireFromString("107.87"), Gross: decimal.RequireFromString("107.87"),
			ServiceFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			ServiceTo:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			Issuer:      models.InvoiceParty{Name: "Betrieb", Address: "Feldweg 1"},
			Recipient:   models.InvoiceParty{Name: "Nachbar", Address: "Dorf 2"},
			Lines: []models.InvoiceLine{{
				Date:  time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
				Label: "Mähen", Unit: "h", Quantity: decimal.RequireFromString("2.345"),
				UnitPrice: decimal.RequireFromString("46.00"), Cost: decimal.RequireFromString("107.87"),
			}},
		},
	}
	neighbor := models.Neighbor{ID: 9, Name: "Nachbar", Email: "NACHBAR@example.at"}
	view, err := newInvoiceDocumentView(invoice, decimal.RequireFromString("7.87"))
	if err != nil {
		t.Fatal(err)
	}
	view.SettlementAsOf = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	document, err := (documentService{smtpFrom: "rechnung@example.at"}).invoiceMail(
		models.Company{Name: "Betrieb"},
		neighbor,
		42,
		view,
	)
	if err != nil {
		t.Fatal(err)
	}
	if document.Subject != "Rechnung 2026-017" || document.Outbox.Subject != document.Subject {
		t.Fatalf("subject = %q/%q, want shared invoice subject", document.Subject, document.Outbox.Subject)
	}
	if document.Outbox.DeliveryKey != "beleg:17:nachbar@example.at" || document.Outbox.BillingYearID != 42 || document.Outbox.InvoiceID != invoice.ID {
		t.Fatalf("outbox identity = %+v", document.Outbox)
	}
	if document.Attachment.Filename != "Rechnung_2026-017.pdf" ||
		document.Outbox.AttName != document.Attachment.Filename ||
		!bytes.HasPrefix(document.Attachment.Data, []byte("%PDF-")) {
		t.Fatalf("attachment = %q (%d bytes), want shared rendered invoice PDF",
			document.Attachment.Filename, len(document.Attachment.Data))
	}
	for _, want := range []string{"Rechnungsbetrag: 107,87 €", "Bereits bezahlt / verrechnet: 100,00 €", "Noch offen: 7,87 €", "13.10.2026 12:00 UTC"} {
		if !strings.Contains(document.Body, want) {
			t.Errorf("mail missing %q: %s", want, document.Body)
		}
	}
	if document.Body != document.Outbox.Body || !bytes.Equal(document.Attachment.Data, document.Outbox.AttData) {
		t.Fatal("durable mail intent differs from delivery document")
	}
}

func TestInvoiceDeliverySettlementDoesNotChangeArchive(t *testing.T) {
	invoice := documentViewFixture()
	before, err := json.Marshal(invoice)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := pdf.RenderInvoice(&invoice)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, remaining, want string
	}{
		{"unpaid", "113.01", "Noch offen: 113,01 €"},
		{"partial", "13.01", "Bereits bezahlt / verrechnet: 100,00 €"},
		{"paid", "0", "Keine weitere Zahlung für diese Rechnung erforderlich."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := newInvoiceDocumentView(invoice, decimal.RequireFromString(tc.remaining))
			if err != nil {
				t.Fatal(err)
			}
			view.SettlementAsOf = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			message, err := (documentService{}).invoiceMail(models.Company{},
				models.Neighbor{ID: invoice.NeighborID, Email: "synthetic@example.invalid"}, invoice.BillingYearID, view)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(message.Body, tc.want) {
				t.Fatalf("body missing %q: %s", tc.want, message.Body)
			}
			if tc.remaining == "0" && strings.Contains(message.Body, "überweisen") {
				t.Fatal("fully settled invoice must not request payment")
			}
		})
	}
	after, err := json.Marshal(invoice)
	if err != nil {
		t.Fatal(err)
	}
	archiveAfter, err := pdf.RenderInvoice(&invoice)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !bytes.Equal(archive, archiveAfter) {
		t.Fatal("delivery changed frozen invoice or archive PDF")
	}
}

func TestInvoiceMailRejectsMismatchedOrInactiveDocument(t *testing.T) {
	invoice := documentViewFixture()
	for _, tc := range []struct {
		name               string
		neighborID, yearID int64
		status             string
	}{
		{"wrong neighbor", invoice.NeighborID + 1, invoice.BillingYearID, "issued"},
		{"wrong year", invoice.NeighborID, invoice.BillingYearID + 1, "issued"},
		{"canceled", invoice.NeighborID, invoice.BillingYearID, "canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invoice.Status = tc.status
			view, err := newInvoiceDocumentView(invoice, decimal.Zero)
			if err != nil {
				t.Fatal(err)
			}
			view.SettlementAsOf = time.Now()
			_, err = (documentService{}).invoiceMail(models.Company{}, models.Neighbor{ID: tc.neighborID}, tc.yearID, view)
			if err == nil {
				t.Fatal("invalid delivery accepted")
			}
		})
	}
}
