package pdf

import (
	"bytes"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

// StatementYear is one billing year's line on the Kontoauszug.
type StatementYear struct {
	Year       int
	Cost       decimal.Decimal
	Hours      decimal.Decimal
	PaidAmount decimal.Decimal
	// Remaining is the unsettled balance; a negative value is a credit owed to
	// the neighbor and is distinct from the year's net service cost.
	Remaining decimal.Decimal
	Paid      bool
	Credit    bool // negative rest: the neighbor holds a Guthaben
}

// StatementData is the multi-year Kontoauszug PDF's content.
type StatementData struct {
	IssuerName    string
	IssuerAddress string
	RecipientName string
	RecipientAddr string
	Rows          []StatementYear
	TotalCost     decimal.Decimal
	TotalHours    decimal.Decimal
	Today         time.Time
	Payments      []StatementPayment
}

// StatementPayment is an actual cash movement, not a second invoice deduction.
type StatementPayment struct {
	Year      int
	Date      time.Time
	Amount    decimal.Decimal
	Reference string
	Reversal  bool
}

// RenderStatement builds an A4 multi-year account statement PDF.
func RenderStatement(s StatementData) ([]byte, error) {
	pdf, err := newDoc()
	if err != nil {
		return nil, err
	}
	pdf.AddPage()
	right := pageW - marginR
	y := 48.0

	gtext(pdf, marginL, y, 15, true, firstNonEmpty(s.IssuerName, "Kontoauszug"))
	gaccent(pdf, marginL, y+18)
	y += 24
	yIss := gblock(pdf, marginL, y, 9.5, s.IssuerAddress)

	gtextR(pdf, right, y, 18, true, "KONTOAUSZUG")
	if !s.Today.IsZero() {
		gtextR(pdf, right, y+22, 10, false, "Stand: "+s.Today.Format("02.01.2006"))
	}

	y = maxf(yIss, y+40) + 16
	gtext(pdf, marginL, y, 9, false, "Für")
	y += 13
	y = gblock(pdf, marginL, y, 10.5, s.RecipientName+"\n"+s.RecipientAddr) + 20

	// table
	colYear := marginL
	colHours := marginL + 145
	colCost := marginL + 270
	colPaid := marginL + 370
	gtext(pdf, colYear, y, 9, true, "Jahr")
	gtextR(pdf, colHours+30, y, 9, true, "Stunden")
	gtextR(pdf, colCost, y, 9, true, "Leistungssaldo")
	gtextR(pdf, colPaid, y, 9, true, "Zahlungen")
	gtextR(pdf, right, y, 9, true, "Offen / Guthaben")
	y += 13
	pdf.SetLineWidth(0.6)
	pdf.SetStrokeColor(120, 120, 120)
	pdf.Line(marginL, y, right, y)
	y += 8

	for _, r := range s.Rows {
		if y > 770 {
			pdf.AddPage()
			y = 56
		}
		gtext(pdf, colYear, y, 9.5, false, strconv.Itoa(r.Year))
		gtextR(pdf, colHours+30, y, 9.5, false, trimZeros(r.Hours)+" h")
		gtextR(pdf, colCost, y, 9.5, false, money(r.Cost))
		gtextR(pdf, colPaid, y, 9.5, false, money(r.PaidAmount))
		gtextR(pdf, right, y, 9.5, false, money(r.Remaining))
		y += 13
		gtext(pdf, colYear, y, 8, false, statementStatus(r))
		y += 16
	}

	// Keep the summary rule + total row together above the footer if the last data
	// row ended near the page bottom.
	if y > 740 {
		pdf.AddPage()
		y = 56
	}
	y += 4
	pdf.Line(colHours, y, right, y)
	y += 10
	gtextR(pdf, colHours+30, y, 10.5, true, trimZeros(s.TotalHours)+" h")
	gtext(pdf, colYear, y, 10, true, "Summe")
	gtextR(pdf, colCost, y, 10, true, money(s.TotalCost))
	paid, remaining := decimal.Zero, decimal.Zero
	for _, row := range s.Rows {
		paid = paid.Add(row.PaidAmount)
		remaining = remaining.Add(row.Remaining)
	}
	gtextR(pdf, colPaid, y, 10, true, money(paid))
	gtextR(pdf, right, y, 10, true, money(remaining))
	y += 26
	if y > 740 {
		pdf.AddPage()
		y = 56
	}
	gtext(pdf, marginL, y, 8, false, "Leistungssaldo ist kein Zahlbetrag. Negative Restbeträge sind Guthaben; Überträge sind keine Zahlungen.")
	y += 28
	if len(s.Payments) > 0 {
		paymentHeader := func() {
			gtext(pdf, marginL, y, 11, true, "Zahlungshistorie - tatsächliche Kontobewegungen")
			y += 20
			gtext(pdf, marginL, y, 8, true, "Datum / Jahr")
			gtext(pdf, marginL+125, y, 8, true, "Art / ursprüngliche Referenz")
			gtextR(pdf, right, y, 8, true, "Betrag")
			y += 18
		}
		if y > 700 {
			pdf.AddPage()
			y = 56
		}
		paymentHeader()
		for _, payment := range s.Payments {
			if y > 750 {
				pdf.AddPage()
				y = 56
				paymentHeader()
			}
			gtext(pdf, marginL, y, 9, false, payment.Date.Format("02.01.2006")+" / "+strconv.Itoa(payment.Year))
			label := "Zahlung"
			if payment.Reversal {
				label = "Gegenbuchung"
			}
			if payment.Reference != "" {
				label += " / " + payment.Reference
			}
			labelLines, err := settlementTextLines(pdf, label, 9, contentW-215)
			if err != nil {
				return nil, err
			}
			gtextR(pdf, right, y, 9, false, money(payment.Amount))
			for _, line := range labelLines {
				if y+12 > 770 {
					pdf.AddPage()
					y = 56
					paymentHeader()
				}
				gtext(pdf, marginL+125, y, 9, false, line)
				y += 12
			}
			y += 6
		}
	}

	gfooter(pdf, s.IssuerName, s.IssuerAddress, "", "")
	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// statementStatus formats settlement state independently from the service cost.
func statementStatus(r StatementYear) string {
	switch {
	case r.Paid:
		return "bezahlt"
	case r.Credit:
		return "Guthaben · " + money(r.Remaining.Neg())
	default:
		return "offen"
	}
}
