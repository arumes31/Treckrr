package pdf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/testutil/pdftext"
)

// TestItemizedInvoiceSettlementPaginationAndArchive paginates many settlement
// rows, each exactly once, keeps the archive PDF byte-identical, and rejects an
// inconsistent supplement.
func TestItemizedInvoiceSettlementPaginationAndArchive(t *testing.T) {
	t.Parallel()
	invoice := paymentTestInvoice()
	archive, err := RenderInvoice(invoice)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]models.SettlementLine, 0, 120)
	for i := range 120 {
		rows = append(rows, models.SettlementLine{Date: invoice.IssuedOn, Kind: "payment", Label: fmt.Sprintf("Teilzahlung %03d", i), Amount: decimal.NewFromInt(1)})
	}
	data, err := RenderInvoiceWithPaymentStatus(invoice, InvoicePaymentStatus{AsOf: invoice.IssuedOn, Remaining: decimal.RequireFromString("157.20"), Lines: rows})
	if err != nil {
		t.Fatal(err)
	}
	text, err := pdftext.Text(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Count(text, row.Label) != 1 {
			t.Fatalf("missing/duplicated %s", row.Label)
		}
	}
	if strings.Count(text, "Einzelnachweis") < 3 || !strings.Contains(text, "157,20 €") {
		t.Fatal("missing pagination or remaining")
	}
	after, err := RenderInvoice(invoice)
	if err != nil || !bytes.Equal(archive, after) {
		t.Fatal("archive changed")
	}
	rows[0].Amount = decimal.NewFromInt(2)
	if _, err := RenderInvoiceWithPaymentStatus(invoice, InvoicePaymentStatus{AsOf: invoice.IssuedOn, Remaining: decimal.RequireFromString("157.20"), Lines: rows}); err == nil {
		t.Fatal("inconsistent supplement accepted")
	}
}

// TestStatementIncludesPaymentHistoryAndRemaining checks the statement PDF shows
// the open balance and the dated payment history.
func TestStatementIncludesPaymentHistoryAndRemaining(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	data, err := RenderStatement(StatementData{Today: date, Rows: []StatementYear{{Year: 2026, Cost: decimal.NewFromInt(300), PaidAmount: decimal.NewFromInt(100), Remaining: decimal.NewFromInt(200)}}, TotalCost: decimal.NewFromInt(300), Payments: []StatementPayment{{Year: 2026, Date: date, Amount: decimal.NewFromInt(100), Reference: "R-2026"}}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := pdftext.Text(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Offen / Guthaben", "200,00 €", "Zahlungshistorie", "14.10.2026 / 2026", "R-2026"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

// TestSettlementTextLinesMeasuresWideAndUnbrokenLabels wraps wide glyphs and
// unbroken labels so that no line exceeds the column width.
func TestSettlementTextLinesMeasuresWideAndUnbrokenLabels(t *testing.T) {
	t.Parallel()
	doc, err := newDoc()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{strings.Repeat("W", 300), strings.Repeat("Änderung einer Gegenleistung ", 50)} {
		lines, err := settlementTextLines(doc, text, 9, 250)
		if err != nil || len(lines) < 2 {
			t.Fatalf("lines=%v, err=%v", lines, err)
		}
		for _, line := range lines {
			width, err := doc.MeasureTextWidth(line)
			if err != nil || width > 250 {
				t.Fatalf("label exceeds column: %s / %f", line, width)
			}
		}
	}
}
