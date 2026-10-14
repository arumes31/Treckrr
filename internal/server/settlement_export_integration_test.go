//go:build integration

package server

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/pdf"
	"github.com/d0linger/treckrr/internal/store"
	"github.com/d0linger/treckrr/internal/testutil/pdftext"
)

func TestItemizedSettlementDeliveryIntegration(t *testing.T) {
	e := newItEnv(t)
	first := issuePaymentDeliveryInvoice(t, e, "100", "-44")
	e.post("/entries", url.Values{"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)}, "entry_date": {"2026-09-21"}, "unit": {"Pauschale"}, "quantity": {"1"}, "unit_price": {"100"}, "task_label": {"SECOND DOCUMENT ONLY"}})
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	invoices, err := e.st.ListActiveInvoices(e.ctx, e.yearID64, e.neighborID)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("invoices=%v, %v", invoices, err)
	}
	second := invoices[1]
	payment, err := e.st.RecordPayment(e.ctx, store.PaymentInput{YearID: e.yearID64, NeighborID: e.neighborID, InvoiceID: second.ID, Amount: decimal.NewFromInt(150), PaidOn: second.IssuedOn, Method: "bar", Note: "PRIVATE PAYMENT SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	before := invoiceGuardSnapshot(t, e)
	archiveBefore, err := pdf.RenderInvoice(&first)
	if err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf("?year=%d&invoice_id=%d", e.yearID64, first.ID)
	page := e.get(fmt.Sprintf("/neighbors/%d/beleg", e.neighborID) + query)
	for _, want := range []string{"Einzelnachweis Zahlung und Verrechnung", "Gegenleistung / Kontoverrechnung", "Zahlung (bar)", "50,00", "83,20"} {
		if !strings.Contains(page, want) {
			t.Fatalf("HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{"PRIVATE PAYMENT SECRET", "SECOND DOCUMENT ONLY"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("invoice leaked %q", forbidden)
		}
	}
	data := getPaymentDeliveryPDF(t, e, e.client, fmt.Sprintf("/neighbors/%d/beleg.pdf", e.neighborID)+query)
	text, err := pdftext.Text(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Einzelnachweis", "Gegenleistung / Kontoverrechnung", "Zahlung (bar)", "-50,00 €", "83,20 €"} {
		if !strings.Contains(text, want) {
			t.Fatalf("PDF missing %q", want)
		}
	}
	if strings.Contains(text, "PRIVATE PAYMENT SECRET") {
		t.Fatal("PDF leaked payment note")
	}
	csvBody := e.get(fmt.Sprintf("/neighbors/%d/settlement.csv", e.neighborID) + query)
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(csvBody, "\ufeff")))
	reader.Comma = ';'
	rows, err := reader.ReadAll()
	if err != nil || len(rows) != 5 {
		t.Fatalf("CSV=%v, %v", rows, err)
	}
	if rows[1][4] != "277,20" || rows[1][6] != "83,20" || rows[4][5] != "-50,00" {
		t.Fatalf("CSV values=%v", rows)
	}
	if strings.Contains(csvBody, second.Number) || strings.Contains(csvBody, "PRIVATE PAYMENT SECRET") {
		t.Fatal("selected CSV leaked another invoice or note")
	}
	payments := e.get(neighborPaymentsURL(e.neighborID, e.yearID64))
	for _, want := range []string{"Aktuelle Zahlungszuordnung", "Angerechnet auf Rechnung " + first.Number, "Angerechnet auf Rechnung " + second.Number} {
		if !strings.Contains(payments, want) {
			t.Fatalf("payment page missing %q", want)
		}
	}
	edit := e.get(fmt.Sprintf("/payments/%d/edit", payment.PaymentID))
	if !strings.Contains(edit, "Aktuelle Zahlungszuordnung") {
		t.Fatal("edit page lacks allocation")
	}
	view, err := (&Server{store: e.st}).loadInvoiceDocumentView(e.ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	neighbor, err := e.st.GetNeighbor(e.ctx, e.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	company, err := e.st.GetCompany(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	mail, err := (documentService{}).invoiceMail(company, *neighbor, e.yearID64, view)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Einzelnachweis", "Zahlung (bar): -50,00 €", "Noch offen: 83,20 €"} {
		if !strings.Contains(mail.Body, want) {
			t.Fatalf("mail missing %q", want)
		}
	}
	if strings.Contains(mail.Body, "PRIVATE PAYMENT SECRET") || strings.Contains(mail.Body, "SECOND DOCUMENT ONLY") {
		t.Fatal("mail leaked private or unrelated data")
	}
	attachment, err := pdftext.Text(mail.Attachment.Data)
	if err != nil || !strings.Contains(attachment, "-50,00 €") {
		t.Fatalf("mail attachment lacks itemized payment: %v", err)
	}
	if after := invoiceGuardSnapshot(t, e); after != before {
		t.Fatal("document reads changed financial values")
	}
	archiveAfter, err := pdf.RenderInvoice(&first)
	if err != nil || !bytes.Equal(archiveBefore, archiveAfter) {
		t.Fatal("archive changed")
	}
	share := e.post(fmt.Sprintf("/neighbors/%d/beleg/share", e.neighborID)+query, url.Values{"days": {"30"}})
	match := regexp.MustCompile(`/s/portal/([a-f0-9]+)`).FindStringSubmatch(share)
	if len(match) != 2 {
		t.Fatal("no share")
	}
	public := &http.Client{Timeout: 10 * time.Second}
	response, err := public.Get(e.srv.URL + "/s/portal/" + match[1])
	if err != nil {
		t.Fatal(err)
	}
	sharedHTML, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK ||
		!bytes.Contains(sharedHTML, []byte("Zahlung (bar)")) ||
		bytes.Contains(sharedHTML, []byte("PRIVATE PAYMENT SECRET")) ||
		bytes.Contains(sharedHTML, []byte("SECOND DOCUMENT ONLY")) {
		t.Fatalf("shared HTML detail invalid: status=%d, %v", response.StatusCode, readErr)
	}
	sharedPDF := getPaymentDeliveryPDF(t, e, public, "/s/portal/"+match[1]+"/invoice.pdf")
	sharedText, err := pdftext.Text(sharedPDF)
	if err != nil || !strings.Contains(sharedText, "-50,00 €") || strings.Contains(sharedText, "PRIVATE PAYMENT SECRET") {
		t.Fatalf("shared detail invalid: %v", err)
	}
}

func TestSettlementCSVAccessIntegration(t *testing.T) {
	e := newItEnv(t)
	invoice := issuePaymentDeliveryInvoice(t, e, "100", "-44")
	path := fmt.Sprintf("/neighbors/%d/settlement.csv?year=%d", e.neighborID, e.yearID64)
	public := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := public.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || !strings.HasPrefix(response.Header.Get("Location"), "/login") {
		t.Fatalf("anonymous CSV access: status=%d, redirect=%q", response.StatusCode, response.Header.Get("Location"))
	}
	for _, tc := range []struct {
		name, suffix string
		status       int
	}{
		{"invalid selection", "&invoice_id=invalid", http.StatusBadRequest},
		{"unknown invoice", "&invoice_id=" + itoa64(invoice.ID+1000), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := e.client.Get(e.srv.URL + path + tc.suffix)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d, want %d", response.StatusCode, tc.status)
			}
		})
	}
}

func TestNeighborWithoutSelectedYearSettlementIntegration(t *testing.T) {
	e := newItEnv(t)
	id, err := e.st.CreateNeighbor(e.ctx, "Unassigned "+e.uname, "")
	if err != nil {
		t.Fatal(err)
	}
	before := invoiceGuardSnapshot(t, e)
	for _, section := range []string{"overview", "payments"} {
		body := e.get(fmt.Sprintf("/neighbors/%d?year=%d&view=%s", id, e.yearID64, section))
		if !strings.Contains(body, "0,00 €") {
			t.Fatalf("%s lost the empty account view", section)
		}
	}
	// Empty account pages remain usable, but no invoice or CSV ownership is
	// granted merely by opening a neighbor in an unrelated billing year.
	for _, path := range []string{
		fmt.Sprintf("/neighbors/%d?year=%d&view=payments&invoice_id=999999", id, e.yearID64),
		fmt.Sprintf("/neighbors/%d/settlement.csv?year=%d", id, e.yearID64),
	} {
		response, err := e.client.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status=%d, want 404", path, response.StatusCode)
		}
	}
	if invoiceGuardSnapshot(t, e) != before {
		t.Fatal("empty account reads changed financial values")
	}
}
