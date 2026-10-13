package pdf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/testutil/pdftext"
)

func paymentTestInvoice() *models.Invoice {
	date := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	return &models.Invoice{
		ID: 2, Number: "2026-002", Kind: "invoice", Status: "issued", IssuedOn: date,
		PaymentReference: "2026-002",
		Content: &models.InvoiceContent{
			Issuer: models.InvoiceParty{
				Name: "Testbetrieb", Address: "Testweg 1\nTestort", IBAN: "AT611904300234573201",
			},
			Recipient: models.InvoiceParty{Name: "Testnachbar", Address: "Testweg 2"},
			Gross:     decimal.RequireFromString("277.20"), Net: decimal.RequireFromString("277.20"),
			TaxNote: "Synthetische Testdaten - keine echte Rechnung.",
			Lines: []models.InvoiceLine{
				{
					Date: date, Label: "Mähen", Unit: "h", Quantity: decimal.NewFromInt(3),
					UnitPrice: decimal.RequireFromString("64.40"), Cost: decimal.RequireFromString("193.20"),
				},
				{
					Date: date, Label: "Mannstunden", Unit: "h", Quantity: decimal.NewFromInt(3),
					UnitPrice: decimal.NewFromInt(28), Cost: decimal.NewFromInt(84),
				},
			},
		},
	}
}

func TestRenderInvoiceWithPaymentStatus(t *testing.T) {
	tests := []struct {
		name       string
		gross      string
		remaining  string
		settled    string
		wantOpen   string
		wantDemand bool
	}{
		{name: "advance before invoice", gross: "277.20", remaining: "177.20", settled: "100,00 €", wantOpen: "177,20 €", wantDemand: true},
		{name: "unpaid", gross: "277.20", remaining: "277.20", settled: "0,00 €", wantOpen: "277,20 €", wantDemand: true},
		{name: "fully paid", gross: "277.20", remaining: "0", settled: "277,20 €", wantOpen: "0,00 €"},
		{name: "payment and credit offset", gross: "277.20", remaining: "132.20", settled: "145,00 €", wantOpen: "132,20 €", wantDemand: true},
		{name: "zero invoice", gross: "0", remaining: "0", settled: "0,00 €", wantOpen: "0,00 €"},
		{name: "negative historical invoice", gross: "-40", remaining: "0", settled: "0,00 €", wantOpen: "0,00 €"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iv := paymentTestInvoice()
			iv.Content.Gross = decimal.RequireFromString(tt.gross)
			data, err := RenderInvoiceWithPaymentStatus(iv, InvoicePaymentStatus{
				AsOf: iv.IssuedOn, Remaining: decimal.RequireFromString(tt.remaining),
			})
			if err != nil {
				t.Fatal(err)
			}
			text, err := pdftext.Text(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"Gesamt\n" + money(iv.Content.Gross),
				"Zahlungsstand vom 13.10.2026",
				"Bereits bezahlt / verrechnet\n" + tt.settled,
				"Noch zu überweisen\n" + tt.wantOpen,
				"Ergänzung zum unveränderten Rechnungsinhalt",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("PDF is missing %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "Betrag: ") {
				t.Errorf("customer copy repeats the archived full-amount payment instruction:\n%s", text)
			}
			if got := strings.Contains(text, "Verwendungszweck: 2026-002"); got != tt.wantDemand {
				t.Errorf("bank payment demand = %v, want %v", got, tt.wantDemand)
			}
			if got := strings.Contains(text, "Keine Zahlung erforderlich."); got == tt.wantDemand {
				t.Errorf("settled notice = %v, want %v", got, !tt.wantDemand)
			}
		})
	}
}

func TestRenderInvoiceWithPaymentStatusCanceled(t *testing.T) {
	iv := paymentTestInvoice()
	iv.Status = "canceled"
	data, err := RenderInvoiceWithPaymentStatus(iv, InvoicePaymentStatus{AsOf: iv.IssuedOn})
	if err != nil {
		t.Fatal(err)
	}
	text, err := pdftext.Text(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Storniert - keine Zahlung erforderlich.") {
		t.Fatalf("missing cancellation notice:\n%s", text)
	}
	for _, forbidden := range []string{"Noch zu überweisen", "Bereits bezahlt / verrechnet", "Verwendungszweck:", "Betrag:"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("canceled invoice contains misleading %q", forbidden)
		}
	}
}

func TestRenderInvoiceWithPaymentStatusPreservesArchiveAndSnapshot(t *testing.T) {
	iv := paymentTestInvoice()
	iv.Content.Hash = "frozen-hash-must-not-change"
	before, err := json.Marshal(iv)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := RenderInvoice(iv)
	if err != nil {
		t.Fatal(err)
	}
	batch, skipped, err := RenderInvoices([]*models.Invoice{iv})
	if err != nil || len(skipped) != 0 {
		t.Fatalf("archive batch: skipped %v, error %v", skipped, err)
	}
	partial, err := RenderInvoiceWithPaymentStatus(iv, InvoicePaymentStatus{
		AsOf: iv.IssuedOn, Remaining: decimal.RequireFromString("177.20"),
	})
	if err != nil {
		t.Fatal(err)
	}
	paid, err := RenderInvoiceWithPaymentStatus(iv, InvoicePaymentStatus{AsOf: iv.IssuedOn.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(archive, partial) || bytes.Equal(partial, paid) {
		t.Fatal("customer copies must reflect the supplied settlement, unlike the archive")
	}
	after, err := json.Marshal(iv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || iv.Content.Hash != "frozen-hash-must-not-change" {
		t.Fatal("rendering modified the invoice or frozen content")
	}
	again, err := RenderInvoice(iv)
	if err != nil {
		t.Fatal(err)
	}
	batchAgain, _, err := RenderInvoices([]*models.Invoice{iv})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archive, again) || !bytes.Equal(batch, batchAgain) {
		t.Fatal("settlement rendering changed archive bytes")
	}
	text, err := pdftext.Text(archive)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Zahlungsstand") || !strings.Contains(text, "Betrag: 277,20 €") {
		t.Fatalf("archival content changed:\n%s", text)
	}
}

func TestRenderInvoiceWithPaymentStatusValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(**models.Invoice, *InvoicePaymentStatus)
	}{
		{name: "nil invoice", mutate: func(iv **models.Invoice, _ *InvoicePaymentStatus) { *iv = nil }},
		{name: "missing snapshot", mutate: func(iv **models.Invoice, _ *InvoicePaymentStatus) { (*iv).Content = nil }},
		{name: "credit document", mutate: func(iv **models.Invoice, _ *InvoicePaymentStatus) { (*iv).Kind = "gutschrift" }},
		{name: "unknown status", mutate: func(iv **models.Invoice, _ *InvoicePaymentStatus) { (*iv).Status = "draft" }},
		{name: "missing date", mutate: func(_ **models.Invoice, s *InvoicePaymentStatus) { s.AsOf = time.Time{} }},
		{name: "negative remainder", mutate: func(_ **models.Invoice, s *InvoicePaymentStatus) { s.Remaining = decimal.NewFromInt(-1) }},
		{name: "remainder exceeds gross", mutate: func(_ **models.Invoice, s *InvoicePaymentStatus) { s.Remaining = decimal.NewFromInt(278) }},
		{name: "subcent remainder", mutate: func(_ **models.Invoice, s *InvoicePaymentStatus) { s.Remaining = decimal.RequireFromString("1.001") }},
		{name: "canceled demand", mutate: func(iv **models.Invoice, _ *InvoicePaymentStatus) { (*iv).Status = "canceled" }},
		{name: "negative historical demand", mutate: func(iv **models.Invoice, _ *InvoicePaymentStatus) { (*iv).Content.Gross = decimal.NewFromInt(-1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iv := paymentTestInvoice()
			status := InvoicePaymentStatus{AsOf: iv.IssuedOn, Remaining: decimal.NewFromInt(1)}
			tt.mutate(&iv, &status)
			if data, err := RenderInvoiceWithPaymentStatus(iv, status); err == nil || len(data) != 0 {
				t.Fatalf("invalid input returned %d PDF bytes, error %v", len(data), err)
			}
		})
	}
}

func TestRenderInvoiceWithPaymentStatusPaginationAndSkonto(t *testing.T) {
	for _, rows := range []int{2, 24, 28, 35, 90} {
		for _, remaining := range []string{"177.20", "0"} {
			t.Run(fmt.Sprintf("%d_rows_remaining_%s", rows, remaining), func(t *testing.T) {
				iv := paymentTestInvoice()
				iv.Content.ShowVAT = true
				iv.Content.SkontoPct = decimal.NewFromInt(2)
				iv.Content.SkontoUntil = iv.IssuedOn.AddDate(0, 0, 10)
				line := iv.Content.Lines[0]
				iv.Content.Lines = make([]models.InvoiceLine, rows)
				for i := range rows {
					iv.Content.Lines[i] = line
				}
				if rows == 90 {
					iv.Content.TaxNote = strings.Repeat("Langer Steuerhinweis zur Prüfung des Seitenumbruchs.\n", 50)
				}
				data, err := RenderInvoiceWithPaymentStatus(iv, InvoicePaymentStatus{
					AsOf: iv.IssuedOn, Remaining: decimal.RequireFromString(remaining),
				})
				if err != nil {
					t.Fatal(err)
				}
				runs, err := pdftext.Extract(data)
				if err != nil {
					t.Fatal(err)
				}
				paymentPage := 0
				foundSkonto := false
				for _, run := range runs {
					if strings.HasPrefix(run.Text, "Zahlungsstand vom") {
						paymentPage = run.Page
					}
					if paymentPage == 0 {
						continue
					}
					if strings.HasPrefix(run.Text, "Testbetrieb  ") && run.Y < 35 {
						// The issuer footer is intentionally below its rule at 806pt.
						continue
					}
					if run.Page != paymentPage || run.Y < 45 {
						t.Errorf("settlement split or colliding with footer: %+v", run)
					}
					if strings.Contains(run.Text, "Festgeschriebene Kondition: 2 % Skonto bis 23.10.2026") {
						foundSkonto = true
					}
				}
				if paymentPage == 0 || !foundSkonto {
					t.Fatalf("missing payment section (%d) or frozen Skonto (%v)", paymentPage, foundSkonto)
				}
				if rows == 2 && paymentPage != 1 {
					t.Errorf("short invoice should fit on one page, got %d", paymentPage)
				}
				if rows == 90 && paymentPage < 3 {
					t.Errorf("long invoice and note should paginate, got %d", paymentPage)
				}
			})
		}
	}
}
