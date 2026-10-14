//go:build integration

package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestFlatRateBookingContractsIntegration(t *testing.T) {
	for _, tc := range []struct {
		name, version, direction string
		ledger                   bool
	}{
		{name: "new own invoice item", version: "3", direction: "out"},
		{name: "old own account posting", version: "2", direction: "out", ledger: true},
		{name: "new counterclaim", version: "3", direction: "in", ledger: true},
		{name: "old counterclaim", version: "2", direction: "in", ledger: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newItEnv(t)
			form := flatRateForm(e, tc.version, tc.direction, "45", "Synthetic material")
			form.Set("idempotency_key", "flat-rate-"+e.uname)
			for range 2 {
				form.Set("csrf_token", e.csrf("/"))
				req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/entries", strings.NewReader(form.Encode()))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Offline-Replay", "1")
				res, err := e.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != http.StatusNoContent {
					t.Fatalf("flat-rate replay status = %d", res.StatusCode)
				}
			}
			entries, err := e.st.ListEntries(e.ctx, e.neighborID, e.yearID64)
			if err != nil {
				t.Fatal(err)
			}
			ledger, err := e.st.ListNeighborLedger(e.ctx, e.yearID64, e.neighborID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.direction == "out" {
				before := invoiceGuardSnapshot(t, e)
				otherVersion := "2"
				if tc.version == "2" {
					otherVersion = "3"
				}
				form.Set("booking_form_version", otherVersion)
				form.Set("csrf_token", e.csrf("/"))
				req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/entries", strings.NewReader(form.Encode()))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("X-Offline-Replay", "1")
				res, err := e.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != http.StatusUnprocessableEntity || res.Header.Get(replayStatusHeader) != "stored" {
					t.Fatalf("cross-contract retry = %d, marker %q", res.StatusCode, res.Header.Get(replayStatusHeader))
				}
				if after := invoiceGuardSnapshot(t, e); after != before {
					t.Fatal("cross-contract retry duplicated or changed stored financial rows")
				}
				form.Set("booking_form_version", tc.version)
			}
			if tc.ledger {
				if len(entries) != 0 || len(ledger) != 1 {
					t.Fatalf("account posting duplicated/reclassified: entries=%d ledger=%d", len(entries), len(ledger))
				}
				want := decimal.NewFromInt(45)
				if tc.direction == "in" {
					want = want.Neg()
				}
				if !ledger[0].Amount.Equal(want) {
					t.Fatalf("ledger amount = %s, want %s", ledger[0].Amount, want)
				}
				// Even a newer submitted version must not change the persisted family.
				form.Set("booking_form_version", "3")
				e.post(fmt.Sprintf("/ledger/%d/update", ledger[0].ID), form)
				_, _, after, err := e.st.GetLedgerEntry(e.ctx, ledger[0].ID)
				if err != nil || !after.Amount.Equal(want) || after.Booking == nil {
					t.Fatalf("historical edit changed family/value: %+v, %v", after, err)
				}
				return
			}
			if len(entries) != 1 || len(ledger) != 0 || entries[0].Unit != "Pauschale" ||
				!entries[0].Quantity.Equal(decimal.NewFromInt(1)) || !entries[0].Cost.Equal(decimal.NewFromInt(45)) {
				t.Fatalf("own flat rate not a quantity-one invoice item: entries=%+v ledger=%+v", entries, ledger)
			}
			// The existing quantity editor handles the stored invoice item.
			form.Set("booking_kind", "quantity")
			form.Set("unit", "__custom")
			form.Set("unit_custom", "Pauschale")
			form.Set("quantity", "1")
			form.Set("unit_price", "46")
			e.post(fmt.Sprintf("/entries/%d/update", entries[0].ID), form)
			updated, err := e.st.GetEntry(e.ctx, entries[0].ID)
			if err != nil || !updated.Cost.Equal(decimal.NewFromInt(46)) {
				t.Fatalf("flat-rate quantity edit: %+v, %v", updated, err)
			}
		})
	}
}

func TestFlatRateInvoiceAndHistoricalBalanceIntegration(t *testing.T) {
	for _, tc := range []struct{ name, version, gross, open string }{
		{name: "new recommendation", version: "3", gross: "322.20", open: "178.20"},
		{name: "unchanged historical contract", version: "2", gross: "277.20", open: "177.20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newItEnv(t)
			company, err := e.st.GetCompany(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			company.TaxMode, company.VATRate, company.TaxNote = "kleinunternehmer", decimal.Zero, "Synthetic notice"
			if err := e.st.UpdateCompany(e.ctx, company); err != nil {
				t.Fatal(err)
			}
			e.post("/entries", url.Values{
				"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
				"entry_date": {"2026-09-08"}, "unit": {"Pauschale"}, "quantity": {"1"},
				"unit_price": {"277.20"}, "task_label": {"Synthetic own work"},
			})
			e.post("/entries", flatRateForm(e, tc.version, "out", "45", "Synthetic material"))
			e.post("/entries", flatRateForm(e, "3", "in", "44", "Synthetic counter-service"))
			e.post(fmt.Sprintf("/neighbors/%d/payments", e.neighborID), url.Values{
				"year_id": {itoa64(e.yearID64)}, "amount": {"100"}, "paid_on": {"2026-09-20"},
			})
			before, err := e.st.AccountRemaining(e.ctx, e.yearID64, e.neighborID)
			if err != nil || before.StringFixed(2) != "178.20" {
				t.Fatalf("pre-invoice balance = %s, %v", before, err)
			}
			e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
			invoice, err := e.st.GetInvoice(e.ctx, e.yearID64, e.neighborID)
			if err != nil || invoice.Content == nil || invoice.Content.Gross.StringFixed(2) != tc.gross {
				t.Fatalf("invoice = %+v, %v; want gross %s", invoice, err, tc.gross)
			}
			remaining, err := e.st.InvoiceDocumentRemaining(e.ctx, invoice.ID)
			if err != nil || remaining.StringFixed(2) != tc.open {
				t.Fatalf("invoice remaining = %s, %v; want %s", remaining, err, tc.open)
			}
			bridge, err := e.st.ReadInvoiceAccountReconciliation(e.ctx, e.yearID64, e.neighborID)
			if err != nil || bridge.OpenInvoices.StringFixed(2) != tc.open || bridge.AnnualRemaining.StringFixed(2) != "178.20" ||
				!bridge.OpenInvoices.Add(bridge.OutsideInvoices).Equal(bridge.AnnualRemaining) {
				t.Fatalf("account bridge = %+v, %v", bridge, err)
			}
			frozen := settlementPreservationSnapshot(t, e)
			account, err := e.st.AccountRemaining(e.ctx, e.yearID64, e.neighborID)
			if err != nil || !account.Equal(before) {
				t.Fatalf("issuance changed annual balance: %s -> %s, %v", before, account, err)
			}
			// Opening the current forms/documents must not migrate historical values.
			page := e.get(fmt.Sprintf("/neighbors/%d/beleg?year=%d&invoice_id=%d", e.neighborID, e.yearID64, invoice.ID))
			if !strings.Contains(page, "Rechnung und Jahreskonto vergleichen") || !strings.Contains(page, "178,20") {
				t.Fatal("authenticated invoice lacks separate account comparison")
			}
			e.get(neighborBookingURL(e.neighborID, e.yearID64))
			if after := settlementPreservationSnapshot(t, e); after != frozen {
				t.Fatal("readers changed financial rows or invoice contents")
			}
			e.post(fmt.Sprintf("/years/%d/status", e.yearID64), url.Values{"status": {"completed"}})
			e.post("/years/mark-paid", url.Values{
				"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
				"paid_on": {"2026-10-14"}, "method": {"bar"},
			})
			if after := settlementPreservationSnapshot(t, e); after != frozen {
				t.Fatal("closing/settling rewrote existing invoice, entries or ledger")
			}
			if final, err := e.st.AccountRemaining(e.ctx, e.yearID64, e.neighborID); err != nil || !final.IsZero() {
				t.Fatalf("book-close-mark-paid workflow failed: %s, %v", final, err)
			}
		})
	}
}

func flatRateForm(e *itEnv, version, direction, amount, label string) url.Values {
	return url.Values{
		"booking_form_version": {version}, "booking_kind": {"fixed"}, "booking_direction": {direction},
		"neighbor_id": {itoa64(e.neighborID)}, "year_id": {itoa64(e.yearID64)},
		"entry_date": {"2026-09-13"}, "task_label": {label}, "amount": {amount},
	}
}

func TestInvoiceFlatRateUsesExistingVATIntegration(t *testing.T) {
	e := newItEnv(t) // The normal fixture applies 13% VAT to own invoice work.
	e.post("/entries", flatRateForm(e, "3", "out", "45", "Synthetic taxable material"))
	e.post("/entries", flatRateForm(e, "3", "in", "44", "Synthetic separate counter-service"))
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	invoice, err := e.st.GetInvoice(e.ctx, e.yearID64, e.neighborID)
	if err != nil || invoice.Content == nil || invoice.Content.Net.StringFixed(2) != "45.00" ||
		invoice.Content.VATAmount.StringFixed(2) != "5.85" || invoice.Content.Gross.StringFixed(2) != "50.85" {
		t.Fatalf("flat-rate VAT does not match ordinary own services: %+v, %v", invoice, err)
	}
	remaining, err := e.st.AccountRemaining(e.ctx, e.yearID64, e.neighborID)
	if err != nil || remaining.StringFixed(2) != "6.85" {
		t.Fatalf("counter-service changed invoice tax base or balance: %s, %v", remaining, err)
	}
}

func TestInvoiceFlatRateWithHelperReplayIntegration(t *testing.T) {
	e := newItEnv(t)
	form := flatRateForm(e, "3", "out", "45", "Synthetic material with helper")
	form.Set("idempotency_key", "flat-helper-"+e.uname)
	form.Set("person_row_id", "0")
	form.Set("person_id", "")
	form.Set("person_name", "Synthetic helper")
	form.Set("person_hours", "1.5")
	form.Set("person_rate", "20")
	form.Set("person_state", "active")
	for range 2 {
		e.post("/entries", form)
	}
	entries, err := e.st.ListEntries(e.ctx, e.neighborID, e.yearID64)
	if err != nil || len(entries) != 2 {
		t.Fatalf("flat-rate/helper replay rows = %+v, %v", entries, err)
	}
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	invoice, err := e.st.GetInvoice(e.ctx, e.yearID64, e.neighborID)
	if err != nil || invoice.Content == nil || len(invoice.Content.Lines) != 2 || invoice.Content.Net.StringFixed(2) != "75.00" {
		t.Fatalf("flat-rate/helper invoice duplicated or lost a component: %+v, %v", invoice, err)
	}
	amounts := map[string]int{}
	for _, line := range invoice.Content.Lines {
		amounts[line.Cost.StringFixed(2)]++
	}
	if amounts["45.00"] != 1 || amounts["30.00"] != 1 {
		t.Fatalf("invoice components = %v", amounts)
	}
}
