package pdf

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/signintech/gopdf"

	"github.com/d0linger/treckrr/internal/models"
)

// A4 layout constants (points).
const (
	pageW    = 595.28
	marginL  = 42.0
	marginR  = 42.0
	contentW = pageW - marginL - marginR
)

// InvoicePaymentStatus is a dated settlement supplement, not part of the frozen
// invoice. Remaining is the invoice-specific outstanding amount after payments,
// credits and offsets, and must be between zero and the positive invoice gross.
type InvoicePaymentStatus struct {
	AsOf      time.Time
	Remaining decimal.Decimal
	Lines     []models.SettlementLine
}

// money formats a decimal in German notation with a trailing € (e.g. 1.234,56 €).
func money(d decimal.Decimal) string {
	s := d.StringFixed(2) // "1234.56" / "-50.00"
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, frac := s, "00"
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	var b strings.Builder
	n := len(intPart)
	for i, c := range intPart {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	out := b.String() + "," + frac + " €"
	if neg {
		out = "-" + out
	}
	return out
}

// RenderInvoice builds the festgeschriebene Rechnung as a one-or-more-page A4 PDF
// from the frozen invoice snapshot — never from live data, so a re-issue is
// byte-stable. iv.Content must be non-nil.
func RenderInvoice(iv *models.Invoice) ([]byte, error) {
	if iv == nil || iv.Content == nil {
		return nil, fmt.Errorf("kein Rechnungs-Snapshot vorhanden")
	}
	pdf, err := newDoc()
	if err != nil {
		return nil, err
	}
	if err := invoicePage(pdf, iv); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderInvoiceWithPaymentStatus adds a current settlement supplement to a
// customer copy without changing the snapshot or the archival RenderInvoice PDF.
func RenderInvoiceWithPaymentStatus(iv *models.Invoice, status InvoicePaymentStatus) ([]byte, error) {
	if iv == nil || iv.Content == nil {
		return nil, fmt.Errorf("invoice payment status requires a frozen snapshot")
	}
	if iv.Kind != "invoice" {
		return nil, fmt.Errorf("invoice payment status does not support document kind %q", iv.Kind)
	}
	if iv.Status != "issued" && iv.Status != "canceled" {
		return nil, fmt.Errorf("invoice payment status does not support status %q", iv.Status)
	}
	if status.AsOf.IsZero() {
		return nil, fmt.Errorf("invoice payment status requires an observation date")
	}
	positiveGross := decimal.Max(iv.Content.Gross, decimal.Zero)
	if status.Remaining.IsNegative() || status.Remaining.GreaterThan(positiveGross) {
		return nil, fmt.Errorf("invoice remaining amount is outside the invoice gross")
	}
	if !status.Remaining.Equal(status.Remaining.Round(2)) {
		return nil, fmt.Errorf("invoice remaining amount must use whole cents")
	}
	if iv.Status == "canceled" && !status.Remaining.IsZero() {
		return nil, fmt.Errorf("canceled invoice must not request a payment")
	}
	applied := decimal.Zero
	if iv.Status == "issued" {
		applied = positiveGross.Sub(status.Remaining)
	}
	lines, err := models.SettlementLines(status.Lines, applied)
	if err != nil {
		return nil, fmt.Errorf("invoice payment lines: %w", err)
	}
	status.Lines = lines

	doc, err := newDoc()
	if err != nil {
		return nil, err
	}
	if err := invoicePageWithPaymentStatus(doc, iv, &status); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := doc.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderInvoices paints SEVERAL invoices into one document, a page each
// (Ausbaukarte 98). A Sammel-PDF is what an operator hands to the tax adviser
// or prints in one go; the ZIP of separate files (Nr. 50) is the archive.
// Documents without a frozen snapshot are skipped and reported by name, never
// silently dropped.
func RenderInvoices(invoices []*models.Invoice) ([]byte, []string, error) {
	pdf, err := newDoc()
	if err != nil {
		return nil, nil, err
	}
	var skipped []string
	pages := 0
	for _, iv := range invoices {
		if iv == nil || iv.Content == nil {
			if iv != nil {
				skipped = append(skipped, iv.Number)
			}
			continue
		}
		if err := invoicePage(pdf, iv); err != nil {
			skipped = append(skipped, iv.Number)
			continue
		}
		pages++
	}
	if pages == 0 {
		return nil, skipped, fmt.Errorf("keine Rechnung mit Snapshot vorhanden")
	}
	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, skipped, err
	}
	return buf.Bytes(), skipped, nil
}

// invoicePage paints one invoice onto a fresh page of an existing document.
func invoicePage(pdf *gopdf.GoPdf, iv *models.Invoice) error {
	return invoicePageWithPaymentStatus(pdf, iv, nil)
}

// invoicePageWithPaymentStatus keeps archival output on the unchanged nil path.
func invoicePageWithPaymentStatus(pdf *gopdf.GoPdf, iv *models.Invoice, status *InvoicePaymentStatus) error {
	c := iv.Content
	pdf.AddPage()
	y := 48.0

	text := func(x, yy, size float64, bold bool, s string) {
		f := fontRegular
		if bold {
			f = fontBold
		}
		_ = pdf.SetFont(f, "", size)
		pdf.SetXY(x, yy)
		_ = pdf.Cell(nil, s)
	}
	// right-aligned text ending at x=right
	textR := func(right, yy, size float64, bold bool, s string) {
		f := fontRegular
		if bold {
			f = fontBold
		}
		_ = pdf.SetFont(f, "", size)
		w, _ := pdf.MeasureTextWidth(s)
		pdf.SetXY(right-w, yy)
		_ = pdf.Cell(nil, s)
	}
	block := func(x, yy, size float64, s string) float64 {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "" {
				yy += size * 0.6
				continue
			}
			text(x, yy, size, false, strings.TrimSpace(line))
			yy += size * 1.25
		}
		return yy
	}

	// Header: issuer (sender) block, left.
	text(marginL, y, 15, true, firstNonEmpty(c.Issuer.Name, "Rechnung"))
	gaccent(pdf, marginL, y+18)
	y += 24
	yIssuer := block(marginL, y, 9.5, c.Issuer.Address)
	if strings.TrimSpace(c.Issuer.TaxID) != "" {
		text(marginL, yIssuer, 9.5, false, "UID/Steuernr.: "+c.Issuer.TaxID)
		yIssuer += 12
	}

	// Invoice meta, right.
	right := pageW - marginR
	textR(right, y, 18, true, "RECHNUNG")
	textR(right, y+22, 10, false, "Nr. "+iv.Number)
	textR(right, y+35, 10, false, "Datum: "+iv.IssuedOn.Format("02.01.2006"))
	if !c.ServiceFrom.IsZero() {
		period := c.ServiceFrom.Format("02.01.2006")
		if !c.ServiceTo.IsZero() && !c.ServiceTo.Equal(c.ServiceFrom) {
			period += "–" + c.ServiceTo.Format("02.01.2006")
		}
		textR(right, y+48, 10, false, "Leistungszeitraum: "+period)
	}

	// Recipient block.
	y = maxf(yIssuer, y+62) + 16
	text(marginL, y, 9, false, "An")
	y += 13
	y = block(marginL, y, 10.5, c.Recipient.Name+"\n"+c.Recipient.Address)
	if strings.TrimSpace(c.Recipient.TaxID) != "" {
		text(marginL, y, 9.5, false, "UID: "+c.Recipient.TaxID)
		y += 12
	}
	y += 14

	// Line-item table header.
	colDate := marginL
	colQty := marginL + 300
	colPrice := marginL + 370
	colCost := pageW - marginR
	pdf.SetLineWidth(0.6)
	pdf.SetStrokeColor(120, 120, 120)
	text(colDate, y, 9, true, "Datum · Leistung")
	textR(colQty+18, y, 9, true, "Menge")
	textR(colPrice+18, y, 9, true, "Einzel")
	textR(colCost, y, 9, true, "Betrag")
	y += 13
	pdf.Line(marginL, y, pageW-marginR, y)
	y += 8

	for _, l := range c.Lines {
		if y > 760 { // new page
			pdf.AddPage()
			y = 56
		}
		label := l.Label
		if label == "" {
			label = "Leistung"
		}
		unit := l.Unit
		if unit == "" || unit == "h" {
			unit = "h"
		}
		text(colDate, y, 9.5, false, l.Date.Format("02.01.2006")+" · "+label)
		textR(colQty+18, y, 9.5, false, trimZeros(l.Quantity)+" "+unit)
		textR(colPrice+18, y, 9.5, false, money(l.UnitPrice))
		textR(colCost, y, 9.5, false, money(l.Cost))
		y += 14
	}

	// Totals. Keep the whole totals + tax-note + payment block together above the
	// footer: if the line list ended near the page bottom there isn't room, so start
	// a fresh page rather than letting it collide with / spill past the footer.
	if y > 640 {
		pdf.AddPage()
		y = 56
	}
	y += 4
	pdf.Line(colQty, y, pageW-marginR, y)
	y += 10
	if c.ShowVAT {
		textR(colPrice+18, y, 9.5, false, "Netto")
		textR(colCost, y, 9.5, false, money(c.Net))
		y += 14
		textR(colPrice+18, y, 9.5, false, "USt "+trimZeros(c.VATRate)+" %")
		textR(colCost, y, 9.5, false, money(c.VATAmount))
		y += 14
	}
	textR(colPrice+18, y, 11, true, "Gesamt")
	textR(colCost, y, 11, true, money(c.Gross))
	y += 22

	if strings.TrimSpace(c.TaxNote) != "" {
		if status == nil {
			y = block(marginL, y, 9, c.TaxNote) + 6
		} else {
			// Customer supplements need room even after unusually long tax notes.
			for _, line := range wrap(c.TaxNote, 100) {
				if y > 770 {
					pdf.AddPage()
					y = 56
				}
				text(marginL, y, 9, false, line)
				y += 12
			}
			y += 6
		}
	}
	if status != nil {
		if err := invoicePaymentBlock(pdf, iv, *status, y); err != nil {
			return err
		}
		gfooter(pdf, c.Issuer.Name, c.Issuer.Address, c.Issuer.TaxID, c.Issuer.IBAN)
		return nil
	}
	// Payment info.
	if strings.TrimSpace(c.Issuer.IBAN) != "" {
		text(marginL, y, 9.5, true, "Zahlung")
		y += 13
		line := "IBAN " + c.Issuer.IBAN
		if strings.TrimSpace(iv.PaymentReference) != "" {
			line += " · Verwendungszweck: " + iv.PaymentReference
		}
		text(marginL, y, 9.5, false, line)
		y += 14
		text(marginL, y, 9.5, false, "Betrag: "+money(c.Gross))
		y += 14
	}
	// Frozen payment terms also apply to cash invoices without an IBAN.
	if c.SkontoPct.IsPositive() && !c.SkontoUntil.IsZero() {
		text(marginL, y, 9.5, false,
			"Bei Zahlung bis "+c.SkontoUntil.Format("02.01.2006")+": "+trimZeros(c.SkontoPct)+" % Skonto")
	}

	gfooter(pdf, c.Issuer.Name, c.Issuer.Address, c.Issuer.TaxID, c.Issuer.IBAN)
	return nil
}

// invoicePaymentBlock keeps the dated live settlement visibly separate from
// immutable invoice totals and keeps the entire supplement clear of the footer.
func invoicePaymentBlock(pdf *gopdf.GoPdf, iv *models.Invoice, status InvoicePaymentStatus, y float64) error {
	c := iv.Content
	hasPayment := status.Remaining.IsPositive()
	hasIBAN := strings.TrimSpace(c.Issuer.IBAN) != ""
	hasSkonto := c.SkontoPct.IsPositive() && !c.SkontoUntil.IsZero()
	// Heading, settled/open rows, explanation, and optional bank/terms lines.
	if len(status.Lines) > 0 {
		var err error
		y, err = settlementTable(pdf, "Rechnung "+iv.Number+" - Einzelnachweis", status.Lines, y+8)
		if err != nil {
			return err
		}
	}
	height := 132.0
	if hasPayment && hasIBAN {
		height += 32
		if strings.TrimSpace(iv.PaymentReference) != "" {
			height += 16
		}
	}
	if hasSkonto {
		height += 16
	}
	if y+height > 790 {
		pdf.AddPage()
		y = 56
		gtext(pdf, marginL, y, 10, true, "Rechnung "+iv.Number)
		y += 22
	}
	y += 10
	pdf.SetStrokeColor(17, 86, 56)
	pdf.SetLineWidth(1)
	pdf.Line(marginL, y, pageW-marginR, y)
	y += 12
	gtext(pdf, marginL, y, 11, true, "Zahlungsstand vom "+status.AsOf.Format("02.01.2006 15:04 MST"))
	y += 23
	if iv.Status == "canceled" {
		gtext(pdf, marginL, y, 11, true, "Storniert - keine Zahlung erforderlich.")
		y += 20
	} else {
		settled := decimal.Max(c.Gross, decimal.Zero).Sub(status.Remaining)
		gtext(pdf, marginL, y, 9.5, false, "Bereits bezahlt / verrechnet")
		gtextR(pdf, pageW-marginR, y, 9.5, false, money(settled))
		y += 20
		gtext(pdf, marginL, y, 11, true, "Noch zu überweisen")
		gtextR(pdf, pageW-marginR, y, 13, true, money(status.Remaining))
		y += 22
		if !hasPayment {
			gtext(pdf, marginL, y, 9.5, false, "Keine Zahlung erforderlich.")
			y += 16
		}
	}
	gtext(pdf, marginL, y, 8.5, false, "Ergänzung zum unveränderten Rechnungsinhalt; Stand zum angegebenen Datum.")
	y += 18
	if hasPayment && hasIBAN {
		gtext(pdf, marginL, y, 9.5, false, "IBAN "+c.Issuer.IBAN)
		y += 16
		if strings.TrimSpace(iv.PaymentReference) != "" {
			gtext(pdf, marginL, y, 9.5, false, "Verwendungszweck: "+iv.PaymentReference)
			y += 16
		}
	}
	if hasSkonto {
		// Terms stay visible as frozen, including after settlement or cancellation.
		line := "Festgeschriebene Kondition: " + trimZeros(c.SkontoPct) + " % Skonto bis " +
			c.SkontoUntil.Format("02.01.2006")
		gtext(pdf, marginL, y, 9, false, line)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func trimZeros(d decimal.Decimal) string {
	s := d.String()
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return strings.ReplaceAll(s, ".", ",")
}
