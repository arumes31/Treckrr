package pdf

import (
	"strings"

	"github.com/signintech/gopdf"

	"github.com/d0linger/treckrr/internal/models"
)

// settlementTable paginates the read-only supplement independently of immutable
// invoice lines. Every page repeats the document reference and table headings.
func settlementTable(pdf *gopdf.GoPdf, title string, lines []models.SettlementLine, y float64) (float64, error) {
	header := func() {
		gtext(pdf, marginL, y, 10, true, title)
		y += 20
		gtext(pdf, marginL, y, 8.5, true, "Datum")
		gtext(pdf, marginL+75, y, 8.5, true, "Angerechnete Zahlung / Verrechnung")
		gtextR(pdf, pageW-marginR, y, 8.5, true, "Betrag")
		y += 18
	}
	if y > 700 {
		pdf.AddPage()
		y = 56
	}
	header()
	for _, line := range lines {
		label, err := settlementTextLines(pdf, line.Label, 9, contentW-175)
		if err != nil {
			return 0, err
		}
		height := float64(len(label))*12 + 8
		if y+height > 770 {
			pdf.AddPage()
			y = 56
			header()
		}
		date := "nicht eindeutig"
		if !line.Date.IsZero() {
			date = line.Date.Format("02.01.2006")
		}
		gtext(pdf, marginL, y, 8, false, date)
		gtextR(pdf, pageW-marginR, y, 9, false, "-"+money(line.Amount))
		for _, text := range label {
			if y+12 > 770 {
				pdf.AddPage()
				y = 56
				header()
			}
			gtext(pdf, marginL+75, y, 9, false, text)
			y += 12
		}
		y += 8
	}
	return y, nil
}

// settlementTextLines measures the actual embedded font and also splits long
// unbroken labels, so UTF-8 text cannot overlap the applied-amount column.
func settlementTextLines(pdf *gopdf.GoPdf, text string, size, width float64) ([]string, error) {
	if err := pdf.SetFont(fontRegular, "", size); err != nil {
		return nil, err
	}
	lines := []string{}
	line := ""
	for _, word := range strings.Fields(text) {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		measured, err := pdf.MeasureTextWidth(candidate)
		if err != nil {
			return nil, err
		}
		if measured <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
			line = ""
		}
		for _, letter := range word {
			candidate = line + string(letter)
			measured, err = pdf.MeasureTextWidth(candidate)
			if err != nil {
				return nil, err
			}
			if measured > width && line != "" {
				lines = append(lines, line)
				line = string(letter)
			} else {
				line = candidate
			}
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines, nil
}
