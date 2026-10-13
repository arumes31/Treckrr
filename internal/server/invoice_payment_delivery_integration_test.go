//go:build integration

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/pdf"
	"github.com/d0linger/treckrr/internal/testutil/pdftext"
)

// TestInvoicePaymentDeliveryIntegration keeps the invoice total immutable while
// every customer-facing delivery shows the amount still payable for that bill.
func TestInvoicePaymentDeliveryIntegration(t *testing.T) {
	e := newItEnv(t)
	invoice := issuePaymentDeliveryInvoice(t, e, "100", "1")
	archive, err := pdf.RenderInvoice(&invoice)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := json.Marshal(invoice)
	if err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf("?year=%d&invoice_id=%d", e.yearID64, invoice.ID)
	pdfPath := fmt.Sprintf("/neighbors/%d/beleg.pdf", e.neighborID) + query
	emailPath := fmt.Sprintf("/neighbors/%d/beleg/email", e.neighborID) + query
	sharePage := e.post(fmt.Sprintf("/neighbors/%d/beleg/share", e.neighborID)+query, url.Values{"days": {"30"}})
	match := regexp.MustCompile(`/s/portal/([a-f0-9]+)`).FindStringSubmatch(sharePage)
	if len(match) != 2 {
		t.Fatal("fresh invoice share was not displayed")
	}
	public := &http.Client{Timeout: 10 * time.Second}
	publicPath := "/s/portal/" + match[1] + "/invoice.pdf"

	initialPDF := getPaymentDeliveryPDF(t, e, e.client, pdfPath)
	assertPaymentDeliveryPDF(t, initialPDF, "100,00 €", "177,20 €")
	assertPaymentDeliveryPDF(t, getPaymentDeliveryPDF(t, e, public, publicPath), "100,00 €", "177,20 €")
	if remaining, err := e.st.AccountRemaining(e.ctx, e.yearID64, e.neighborID); err != nil ||
		!remaining.Equal(decimal.RequireFromString("178.20")) {
		t.Fatalf("annual balance = %s, error=%v; want 178.20 including the separate ledger claim", remaining, err)
	}

	e.post(emailPath, url.Values{})
	type mailSnapshot struct {
		Body, MessageID string
		Attachment      []byte
	}
	readMail := func() mailSnapshot {
		t.Helper()
		var snapshot mailSnapshot
		if err := e.pool.QueryRowContext(e.ctx, `SELECT body,message_id,att_data FROM mail_outbox
			WHERE delivery_key=$1`, fmt.Sprintf("beleg:%d:synthetic@example.invalid", invoice.ID)).
			Scan(&snapshot.Body, &snapshot.MessageID, &snapshot.Attachment); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	queued := readMail()
	for _, want := range []string{
		"Bereits bezahlt / verrechnet: 100,00 €", "Noch offen: 177,20 €", "Zahlungsstand dieser Rechnung (Stand ",
	} {
		if !strings.Contains(queued.Body, want) {
			t.Errorf("mail body lacks %q", want)
		}
	}
	assertPaymentDeliveryPDF(t, queued.Attachment, "100,00 €", "177,20 €")
	if strings.Contains(queued.Body, "178,20 €") {
		t.Fatal("mail requests the annual balance instead of the selected invoice balance")
	}

	for _, step := range []struct {
		name, payment, paid, open, account string
	}{
		{"partial", "50", "150,00 €", "127,20 €", "128.20"},
		{"fully settled", "127.20", "277,20 €", "0,00 €", "1.00"},
		{"account credit", "25", "277,20 €", "0,00 €", "-24.00"},
	} {
		t.Run(step.name, func(t *testing.T) {
			e.post(fmt.Sprintf("/neighbors/%d/payments", e.neighborID), url.Values{
				"year_id": {itoa64(e.yearID64)}, "amount": {step.payment},
				"invoice_id": {itoa64(invoice.ID)}, "paid_on": {"2026-09-21"},
			})
			currentPDF := getPaymentDeliveryPDF(t, e, e.client, pdfPath)
			assertPaymentDeliveryPDF(t, currentPDF, step.paid, step.open)
			if bytes.Equal(currentPDF, initialPDF) {
				t.Fatal("customer PDF did not refresh the settlement status")
			}
			assertPaymentDeliveryPDF(t, getPaymentDeliveryPDF(t, e, public, publicPath), step.paid, step.open)
			if remaining, err := e.st.AccountRemaining(e.ctx, e.yearID64, e.neighborID); err != nil ||
				!remaining.Equal(decimal.RequireFromString(step.account)) {
				t.Fatalf("account remaining=%s, error=%v; want %s", remaining, err, step.account)
			}
		})
	}

	// Repeated POSTs cannot rewrite the queued message or its dated payment
	// snapshot; ordinary retries must send the exact same content and identity.
	e.post(emailPath, url.Values{})
	retained := readMail()
	if retained.Body != queued.Body || retained.MessageID != queued.MessageID ||
		!bytes.Equal(retained.Attachment, queued.Attachment) {
		t.Fatal("an idempotent mail request rewrote the queued payment snapshot")
	}
	unchanged, err := e.st.GetInvoiceDocument(e.ctx, invoice.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(unchanged)
	if err != nil || !bytes.Equal(stored, frozen) {
		t.Fatal("delivery or payment modified the invoice JSON/hash")
	}
	archiveAfter, err := pdf.RenderInvoice(&unchanged)
	if err != nil || !bytes.Equal(archiveAfter, archive) {
		t.Fatal("delivery or payment modified the frozen archive PDF")
	}
}

func TestInvoicePaymentDeliveryIncludesOffsetsAndCreditsIntegration(t *testing.T) {
	e := newItEnv(t)
	invoice := issuePaymentDeliveryInvoice(t, e, "100", "-20")
	if _, err := e.st.GutschriftInvoiceByID(e.ctx, e.yearID64, e.neighborID, invoice.ID,
		decimal.NewFromInt(10), "Synthetic document credit"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/neighbors/%d/beleg.pdf?year=%d&invoice_id=%d", e.neighborID, e.yearID64, invoice.ID)
	assertPaymentDeliveryPDF(t, getPaymentDeliveryPDF(t, e, e.client, path), "130,00 €", "147,20 €")
	view, err := (&Server{store: e.st}).loadInvoiceDocumentView(e.ctx, invoice)
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
	message, err := (documentService{}).invoiceMail(company, *neighbor, e.yearID64, view)
	if err != nil {
		t.Fatal(err)
	}
	assertPaymentDeliveryPDF(t, message.Attachment.Data, "130,00 €", "147,20 €")
	for _, want := range []string{"Bereits bezahlt / verrechnet: 130,00 €", "Noch offen: 147,20 €"} {
		if !strings.Contains(message.Body, want) {
			t.Errorf("credited mail body lacks %q", want)
		}
	}
}

// issuePaymentDeliveryInvoice models a payment recorded before invoice issuance;
// ledger postings stay on the account, never become invoice lines.
func issuePaymentDeliveryInvoice(t *testing.T, e *itEnv, prepaid, ledger string) models.Invoice {
	t.Helper()
	company, err := e.st.GetCompany(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	company.TaxMode, company.VATRate = "kleinunternehmer", decimal.Zero
	company.TaxNote = "Synthetic invoice notice"
	if err := e.st.UpdateCompany(e.ctx, company); err != nil {
		t.Fatal(err)
	}
	neighbor, err := e.st.GetNeighbor(e.ctx, e.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpdateNeighbor(e.ctx, e.neighborID, neighbor.Name, "", neighbor.Address, "",
		"synthetic@example.invalid", "", nil); err != nil {
		t.Fatal(err)
	}
	e.post("/entries", url.Values{
		"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
		"entry_date": {"2026-09-08"}, "unit": {"Pauschale"}, "quantity": {"1"},
		"unit_price": {"277.20"}, "task_label": {"Synthetic frozen invoice work"},
	})
	e.post(fmt.Sprintf("/neighbors/%d/payments", e.neighborID), url.Values{
		"year_id": {itoa64(e.yearID64)}, "amount": {prepaid}, "paid_on": {"2026-09-20"},
	})
	date := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if _, err := e.st.AddNeighborLedger(e.ctx, e.yearID64, e.neighborID,
		decimal.RequireFromString(ledger), "Separate annual ledger posting", date); err != nil {
		t.Fatal(err)
	}
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	invoice, err := e.st.GetInvoice(e.ctx, e.yearID64, e.neighborID)
	if err != nil || invoice.Content == nil || !invoice.Content.Gross.Equal(decimal.RequireFromString("277.20")) {
		t.Fatalf("fixture invoice does not have frozen gross 277.20: %+v, error=%v", invoice, err)
	}
	return invoice
}

func getPaymentDeliveryPDF(t *testing.T, e *itEnv, client *http.Client, path string) []byte {
	t.Helper()
	response, err := client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("GET %s: status=%d type=%q", path, response.StatusCode, response.Header.Get("Content-Type"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("GET %s: live settlement PDF must not be cached", path)
	}
	blob, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func assertPaymentDeliveryPDF(t *testing.T, blob []byte, paid, remaining string) {
	t.Helper()
	text, err := pdftext.Text(blob)
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"Synthetic frozen invoice work", "Gesamt 277,20 €", "Zahlungsstand vom",
		"Bereits bezahlt / verrechnet " + paid, "Noch zu überweisen " + remaining,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("PDF lacks %q; extracted text: %s", want, text)
		}
	}
	if strings.Contains(text, "Separate annual ledger posting") || strings.Contains(text, "178,20 €") {
		t.Error("selected invoice PDF leaked annual account data")
	}
	if remaining == "0,00 €" && !strings.Contains(text, "Keine Zahlung erforderlich.") {
		t.Error("settled invoice PDF still requests payment")
	}
}
