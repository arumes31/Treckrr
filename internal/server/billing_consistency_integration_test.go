//go:build integration

package server

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

func TestInvoicePaymentFormContextIntegration(t *testing.T) {
	e := newItEnv(t)
	first := issuePaymentDeliveryInvoice(t, e, "100", "1")
	e.post("/entries", url.Values{
		"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
		"entry_date": {"2026-09-21"}, "unit": {"Pauschale"}, "quantity": {"1"},
		"unit_price": {"100"}, "task_label": {"Synthetic second invoice"},
	})
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	invoices, err := e.st.ListActiveInvoices(e.ctx, e.yearID64, e.neighborID)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("invoices=%v, %v", invoices, err)
	}
	second := invoices[1]
	link := fmt.Sprintf("/neighbors/%d?year=%d&amp;view=payments&amp;invoice_id=%d#zahlung-erfassen", e.neighborID, e.yearID64, second.ID)
	if html := e.get(invoiceDocumentURL(second)); !strings.Contains(html, link) {
		t.Fatal("payment link lost invoice selection")
	}
	path := fmt.Sprintf("/neighbors/%d?year=%d&view=payments&invoice_id=%d", e.neighborID, e.yearID64, second.ID)
	form := e.get(path)
	for _, expected := range []string{
		`name="amount" data-payment-amount value="100.00"`,
		fmt.Sprintf(`value="%d" data-remaining="100.00" selected`, second.ID),
		"offen 100,00", "von 100,00",
	} {
		if !strings.Contains(form, expected) {
			t.Fatalf("payment form missing %s", expected)
		}
	}
	e.post(fmt.Sprintf("/neighbors/%d/payments", e.neighborID), url.Values{
		"year_id": {itoa64(e.yearID64)}, "invoice_id": {itoa64(second.ID)},
		"amount": {"100.00"}, "paid_on": {"2026-09-22"},
	})
	remaining, err := e.st.InvoiceDocumentRemaining(e.ctx, first.ID)
	if err != nil || !remaining.Equal(decimal.RequireFromString("177.20")) {
		t.Fatalf("other invoice was paid: %s, %v", remaining, err)
	}
	// Settled invoices must stay in the allocation projection and must not
	// make the payment page for the account or another invoice unreachable.
	paidForm := e.get(path)
	if !strings.Contains(paidForm, `name="amount" data-payment-amount required`) ||
		!strings.Contains(paidForm, fmt.Sprintf(`value="%d" data-remaining="0.00" selected`, second.ID)) {
		t.Fatal("settled invoice was omitted from the payment form")
	}
	accountForm := e.get(fmt.Sprintf("/neighbors/%d?year=%d&view=payments", e.neighborID, e.yearID64))
	if !strings.Contains(accountForm, `name="amount" data-payment-amount value="178.20"`) {
		t.Fatal("settled invoice made the remaining account payment unreachable")
	}
	for _, suffix := range []string{"/mahnung", "/mahnung.pdf", "/mahnung/epc-qr.png"} {
		assertBillingGETStatus(t, e, fmt.Sprintf("/neighbors/%d%s?year=%d&invoice_id=%d&stufe=1", e.neighborID, suffix, e.yearID64, second.ID), http.StatusNotFound)
	}
	for _, invalid := range []string{"invalid", "0", "999999999", itoa64(first.ID) + "&invoice_id=" + itoa64(second.ID)} {
		want := http.StatusBadRequest
		if invalid == "999999999" {
			want = http.StatusNotFound
		}
		assertBillingGETStatus(t, e, fmt.Sprintf("/neighbors/%d?year=%d&view=payments&invoice_id=%s", e.neighborID, e.yearID64, invalid), want)
	}
}

func TestInvoiceXMLSettlementAndFrozenTermIntegration(t *testing.T) {
	e := newItEnv(t)
	company, err := e.st.GetCompany(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	company.TaxMode, company.VATRate, company.PaymentTermDays = "kleinunternehmer", decimal.Zero, 14
	company.TaxID, company.IBAN = "ATU12345678", "AT611904300234573201"
	company.EInvoiceStreet, company.EInvoiceZIP, company.EInvoiceTown, company.EInvoiceCountryCode = "Synthetic Street 1", "4780", "Synthetic town", "AT"
	if err := e.st.UpdateCompany(e.ctx, company); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.ExecContext(e.ctx, `UPDATE neighbors SET tax_id='ATU87654321',
		einvoice_street='Synthetic Street 2',einvoice_zip='4780',einvoice_town='Synthetic town',
		einvoice_country_code='AT',einvoice_order_id='SYNTHETIC-ORDER' WHERE id=$1`, e.neighborID); err != nil {
		t.Fatal(err)
	}
	e.post("/entries", url.Values{
		"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
		"entry_date": {"2026-09-08"}, "unit": {"Pauschale"}, "quantity": {"1"},
		"unit_price": {"277.20"}, "task_label": {"Synthetic XML work"},
	})
	if err := e.st.AddPayment(e.ctx, e.yearID64, e.neighborID, decimal.NewFromInt(100), time.Now(), "synthetic advance", "bank"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AddNeighborLedger(e.ctx, e.yearID64, e.neighborID, decimal.NewFromInt(1), "Separate ledger claim", time.Now()); err != nil {
		t.Fatal(err)
	}
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	invoice, err := e.st.GetInvoice(e.ctx, e.yearID64, e.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(invoice.Content)
	if _, err := e.pool.ExecContext(e.ctx, `UPDATE company SET payment_term_days=90`); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ payment, paid, open string }{
		{"", "100.00", "177.20"}, {"50", "150.00", "127.20"}, {"127.20", "277.20", "0.00"}, {"25", "277.20", "0.00"},
	} {
		if step.payment != "" {
			if err := e.st.AddPayment(e.ctx, e.yearID64, e.neighborID, decimal.RequireFromString(step.payment), time.Now(), "synthetic", "bank"); err != nil {
				t.Fatal(err)
			}
		}
		var data struct {
			Gross string `xml:"TotalGrossAmount"`
			Paid  string `xml:"PrepaidAmount"`
			Open  string `xml:"PayableAmount"`
			Due   string `xml:"PaymentConditions>DueDate"`
		}
		body := e.get(fmt.Sprintf("/documents/%d/ebinterface.xml", invoice.ID))
		if err := xml.Unmarshal([]byte(body), &data); err != nil {
			t.Fatal(err)
		}
		if data.Gross != "277.20" || data.Paid != step.paid || data.Open != step.open || data.Due != invoice.IssuedOn.AddDate(0, 0, 14).Format("2006-01-02") {
			t.Fatalf("XML payment/term mismatch: %+v", data)
		}
		if step.open == "0.00" && strings.Contains(body, "UniversalBankTransaction") {
			t.Fatal("settled XML requests a bank transfer")
		}
	}
	reloaded, err := e.st.GetInvoiceDocument(e.ctx, invoice.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(reloaded.Content)
	if string(before) != string(after) {
		t.Fatal("XML export or payments changed frozen content")
	}
}

func TestDunningAllYearsCSVScopeIntegration(t *testing.T) {
	e := newItEnv(t)
	if _, err := e.pool.ExecContext(e.ctx, `UPDATE company SET tax_mode='kleinunternehmer',vat_rate=0`); err != nil {
		t.Fatal(err)
	}
	nextBase, err := e.st.CreateEmptyBase(e.ctx, e.year+1, "Synthetic CSV "+e.uname)
	if err != nil {
		t.Fatal(err)
	}
	nextYear, err := e.st.CreateBillingYear(e.ctx, e.year+1, nextBase, "Synthetic CSV "+e.uname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM invoice_entries WHERE invoice_id IN (SELECT id FROM invoices WHERE billing_year_id=$1)`,
			`DELETE FROM invoices WHERE billing_year_id=$1`,
			`DELETE FROM entries WHERE billing_year_id=$1`,
			`DELETE FROM billing_year_neighbors WHERE billing_year_id=$1`,
			`DELETE FROM billing_years WHERE id=$1`,
		} {
			if _, err := e.pool.ExecContext(e.ctx, query, nextYear); err != nil {
				t.Error(err)
			}
		}
		if _, err := e.pool.ExecContext(e.ctx, `DELETE FROM price_bases WHERE id=$1`, nextBase); err != nil {
			t.Error(err)
		}
	})
	if err := e.st.AddNeighborToYear(e.ctx, nextYear, e.neighborID); err != nil {
		t.Fatal(err)
	}
	issuedOn := time.Now().AddDate(0, 0, -90)
	for _, year := range []struct {
		id     int64
		number int
		amount int64
	}{
		{e.yearID64, e.year, 100}, {nextYear, e.year + 1, 200},
	} {
		_, err := e.st.CreateEntry(e.ctx, &models.Entry{
			BillingYearID: year.id, NeighborID: e.neighborID, Date: issuedOn,
			TaskLabel: "Synthetic CSV work", Unit: "Pauschale", Quantity: decimal.NewFromInt(1),
			UnitPrice: decimal.NewFromInt(year.amount), Cost: decimal.NewFromInt(year.amount),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.IssueInvoice(e.ctx, year.id, e.neighborID, year.number, issuedOn); err != nil {
			t.Fatal(err)
		}
	}
	base := fmt.Sprintf("/mahnwesen/export.csv?year=%d", e.yearID64)
	for _, tc := range []struct {
		suffix        string
		rows, columns int
	}{{"", 2, 6}, {"&scope=alle", 3, 7}} {
		reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(e.get(base+tc.suffix), "\ufeff")))
		reader.Comma = ';'
		rows, err := reader.ReadAll()
		if err != nil || len(rows) == 0 || len(rows[0]) != tc.columns {
			t.Fatalf("CSV scope mismatch: %v, %v", rows, err)
		}
		// Other synthetic fixtures can coexist in the all-years export.
		var ownRows [][]string
		for _, row := range rows[1:] {
			if strings.Contains(row[0], e.uname) {
				ownRows = append(ownRows, row)
			}
		}
		if len(ownRows) != tc.rows-1 || ownRows[0][5] != "100,00" || (len(ownRows) == 2 && ownRows[1][5] != "200,00") {
			t.Fatalf("CSV own-account amounts mismatch: %v", ownRows)
		}
		if tc.columns == 7 && (ownRows[0][6] == ownRows[1][6] || rows[0][6] != "Abrechnungsjahr") {
			t.Fatal("cross-year export lost year labels")
		}
	}
	page := e.get(fmt.Sprintf("/mahnwesen?year=%d&scope=alle", e.yearID64))
	if !strings.Contains(page, fmt.Sprintf("/mahnwesen/export.csv?year=%d&amp;scope=alle", e.yearID64)) {
		t.Fatal("export link lost scope")
	}
}

func assertBillingGETStatus(t *testing.T, e *itEnv, path string, want int) {
	t.Helper()
	response, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("GET %s=%d want=%d", path, response.StatusCode, want)
	}
}
