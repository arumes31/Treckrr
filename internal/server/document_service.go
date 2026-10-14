package server

import (
	"fmt"
	"strings"

	"github.com/d0linger/treckrr/internal/mail"
	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/pdf"
	"github.com/d0linger/treckrr/internal/store"
	"github.com/d0linger/treckrr/internal/web"
)

// documentService combines frozen invoice content with a separate, dated
// settlement projection for customer delivery. Archive exports bypass this
// service so their original invoice bytes remain unchanged.
type documentService struct {
	smtpFrom string
}

type invoiceMailDocument struct {
	Subject    string
	Body       string
	Attachment mail.Attachment
	Outbox     store.OutboxMail
}

func (s *Server) documents() documentService {
	return documentService{smtpFrom: s.cfg.SMTPFrom}
}

func (documentService) invoicePDF(view invoiceDocumentView) ([]byte, error) {
	blob, err := pdf.RenderInvoiceWithPaymentStatus(&view.Invoice, pdf.InvoicePaymentStatus{
		AsOf: view.SettlementAsOf, Remaining: view.Remaining,
		Lines: view.SettlementLines,
	})
	if err != nil {
		return nil, fmt.Errorf("render invoice pdf: %w", err)
	}
	return blob, nil
}

func (s documentService) invoiceMail(
	company models.Company,
	neighbor models.Neighbor,
	yearID int64,
	view invoiceDocumentView,
) (invoiceMailDocument, error) {
	invoice := view.Invoice
	if invoice.NeighborID != neighbor.ID || invoice.BillingYearID != yearID || !view.Active {
		return invoiceMailDocument{}, fmt.Errorf("%w: invalid delivery account or inactive invoice", errInvalidInvoiceDocument)
	}
	blob, err := s.invoicePDF(view)
	if err != nil {
		return invoiceMailDocument{}, err
	}
	subject := "Rechnung " + invoice.Number
	// Delivery address/settings remain current; document parties remain frozen.
	company.Name, company.Address, company.TaxID, company.IBAN = invoice.Content.Issuer.Name,
		invoice.Content.Issuer.Address, invoice.Content.Issuer.TaxID, invoice.Content.Issuer.IBAN
	intro := fmt.Sprintf("anbei die Rechnung %s als PDF.\n\nZahlungsstand dieser Rechnung (Stand %s):\n"+
		"Rechnungsbetrag: %s\nBereits bezahlt / verrechnet: %s\nNoch offen: %s",
		invoice.Number, view.SettlementAsOf.Format("02.01.2006 15:04 MST"),
		web.Money(invoice.Content.Gross), web.Money(view.AppliedSettlement), web.Money(view.Remaining))
	if len(view.SettlementLines) > 0 {
		intro += "\n\nEinzelnachweis (nur auf diese Rechnung angerechnete Beträge):"
		for _, line := range view.SettlementLines {
			date := "Datum nicht eindeutig zuordenbar"
			if !line.Date.IsZero() {
				date = line.Date.Format("02.01.2006")
			}
			intro += "\n" + date + " · " + line.Label + ": -" + web.Money(line.Amount)
		}
	}
	if view.Remaining.IsZero() {
		intro += "\nKeine weitere Zahlung für diese Rechnung erforderlich."
	} else {
		intro += "\nBitte nur den noch offenen Betrag dieser Rechnung überweisen."
	}
	body := mailBody(company, invoice.Content.Recipient.Name, intro)
	attachment := mail.Attachment{
		Filename:    "Rechnung_" + sanitizeFilename(invoice.Number) + ".pdf",
		ContentType: "application/pdf",
		Data:        blob,
	}
	messageID := mail.StableMessageID(
		s.smtpFrom,
		neighbor.Email,
		subject,
		body,
		[]mail.Attachment{attachment},
	)
	return invoiceMailDocument{
		Subject:    subject,
		Body:       body,
		Attachment: attachment,
		Outbox: store.OutboxMail{
			Kind: "beleg", NeighborID: neighbor.ID, BillingYearID: yearID, InvoiceID: invoice.ID,
			Recipient: neighbor.Email, Subject: subject, Body: body,
			AttName: attachment.Filename, AttType: attachment.ContentType, AttData: attachment.Data,
			DeliveryKey: fmt.Sprintf("beleg:%d:%s", invoice.ID,
				strings.ToLower(strings.TrimSpace(neighbor.Email))),
			MessageID: messageID, RetryFailed: true, ForceResend: true,
		},
	}, nil
}
