//go:build integration

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestInvoiceCreditValidationRedirectIntegration keeps rejected corrections on
// the selected document and proves that no financial records were changed.
func TestInvoiceCreditValidationRedirectIntegration(t *testing.T) {
	e := newItEnv(t)
	nid, yid := e.neighborID, e.yearID64
	for range 2 {
		e.book(nid, "2026-03-01")
		e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})
	}
	invoices, err := e.st.ListActiveInvoices(e.ctx, yid, nid)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("two active invoices: count=%d, err=%v", len(invoices), err)
	}
	selected := invoices[1]
	e.post(fmt.Sprintf("/neighbors/%d/payments", nid), url.Values{
		"year_id": {itoa64(yid)}, "invoice_id": {itoa64(selected.ID)},
		"amount": {"5"}, "paid_on": {"2026-03-02"},
	})
	foreignID := e.secondNeighbor("IT-Foreign")
	e.book(foreignID, "2026-03-01")
	e.postIssue(foreignID, url.Values{"year_id": {itoa64(yid)}})
	foreign, err := e.st.GetInvoice(e.ctx, yid, foreignID)
	if err != nil {
		t.Fatal(err)
	}
	foreignBefore, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	before := invoiceGuardSnapshot(t, e)
	remaining, err := e.st.AccountRemaining(e.ctx, yid, nid)
	if err != nil {
		t.Fatal(err)
	}
	client := *e.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	path := fmt.Sprintf("/neighbors/%d/invoice/gutschrift", nid)
	for _, tc := range []struct {
		name, amount, note, flash string
	}{
		{name: "subcent", amount: "0.001", flash: msgMoneyCents},
		{
			name: "oversized amount", amount: strings.Repeat("1", maxDecimalLen+1),
			flash: fmt.Sprintf("Betrag darf höchstens %d Zeichen lang sein.", maxDecimalLen),
		},
		{
			name: "oversized reason", amount: "1", note: strings.Repeat("x", maxNoteLen+1),
			flash: fmt.Sprintf("Grund darf höchstens %d Zeichen lang sein.", maxNoteLen),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testEnv := *e
			e := &testEnv
			e.t = t
			form := url.Values{
				"year_id": {itoa64(yid)}, "invoice_id": {itoa64(selected.ID)},
				"amount": {tc.amount}, "note": {tc.note}, "csrf_token": {e.csrf("/")},
			}
			response, err := client.PostForm(e.srv.URL+path, form)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			location := response.Header.Get("Location")
			if response.StatusCode != http.StatusSeeOther || location != invoiceDocumentURL(selected) {
				t.Fatalf("response=%d %q, want selected invoice redirect", response.StatusCode, location)
			}
			page := e.get(location)
			if !strings.Contains(page, tc.flash) || !strings.Contains(page, selected.Number) {
				t.Fatalf("selected invoice does not show validation message %q", tc.flash)
			}
			if after := invoiceGuardSnapshot(t, e); after != before {
				t.Fatal("rejected correction changed stored financial records")
			}
		})
	}
	for _, tc := range []struct {
		name string
		ids  []string
		code int
	}{
		{name: "malformed selection", ids: []string{"broken"}, code: http.StatusBadRequest},
		{
			name: "duplicate selection", ids: []string{itoa64(selected.ID), itoa64(invoices[0].ID)},
			code: http.StatusBadRequest,
		},
		{name: "foreign document", ids: []string{itoa64(foreign.ID)}, code: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testEnv := *e
			e := &testEnv
			e.t = t
			response, err := client.PostForm(e.srv.URL+path, url.Values{
				"year_id": {itoa64(yid)}, "invoice_id": tc.ids,
				"amount": {"1"}, "csrf_token": {e.csrf("/")},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.code || response.Header.Get("Location") != "" {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf(
					"response=%d %q, want %d: %s",
					response.StatusCode,
					response.Header.Get("Location"),
					tc.code,
					body,
				)
			}
			if after := invoiceGuardSnapshot(t, e); after != before {
				t.Fatal("invalid selection changed stored financial records")
			}
		})
	}
	t.Run("foreign selection with invalid amount", func(t *testing.T) {
		testEnv := *e
		e := &testEnv
		e.t = t
		response, err := client.PostForm(e.srv.URL+path, url.Values{
			"year_id": {itoa64(yid)}, "invoice_id": {itoa64(foreign.ID)},
			"amount": {"0.001"}, "csrf_token": {e.csrf("/")},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		location := response.Header.Get("Location")
		want := fmt.Sprintf(
			"/neighbors/%d/beleg?year=%d&invoice_id=%d",
			nid,
			yid,
			foreign.ID,
		)
		if response.StatusCode != http.StatusSeeOther || location != want {
			t.Fatalf("response=%d %q, want validation redirect within the route account", response.StatusCode, location)
		}
		page, err := e.client.Get(e.srv.URL + location)
		if err != nil {
			t.Fatal(err)
		}
		defer page.Body.Close()
		body, err := io.ReadAll(page.Body)
		if err != nil {
			t.Fatal(err)
		}
		if page.StatusCode != http.StatusNotFound || strings.Contains(string(body), foreign.Number) {
			t.Fatalf("foreign selection after redirect: status=%d, want 404 without document disclosure", page.StatusCode)
		}
	})
	t.Run("stale form after closing", func(t *testing.T) {
		testEnv := *e
		e := &testEnv
		e.t = t
		e.post(fmt.Sprintf("/years/%d/status", yid), url.Values{"status": {"completed"}})
		year, err := e.st.GetBillingYear(e.ctx, yid)
		if err != nil || !year.Completed() {
			t.Fatalf("year completion: %v", err)
		}
		response, err := client.PostForm(e.srv.URL+path, url.Values{
			"year_id": {itoa64(yid)}, "invoice_id": {itoa64(selected.ID)},
			"amount": {"1"}, "csrf_token": {e.csrf("/")},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		location := response.Header.Get("Location")
		if response.StatusCode != http.StatusSeeOther || location != invoiceDocumentURL(selected) {
			t.Fatalf("response=%d %q, want selected invoice redirect", response.StatusCode, location)
		}
		page := e.get(location)
		if !strings.Contains(page, "für eine Korrektur bitte zuerst wieder öffnen") ||
			!strings.Contains(page, selected.Number) {
			t.Fatal("selected invoice does not explain the closed year")
		}
	})
	if after := invoiceGuardSnapshot(t, e); after != before {
		t.Fatal("rejected corrections or year closing changed stored financial records")
	}
	if after, err := e.st.AccountRemaining(e.ctx, yid, nid); err != nil || !after.Equal(remaining) {
		t.Fatalf(
			"account balance changed: before=%s after=%s err=%v",
			remaining,
			after,
			err,
		)
	}
	foreignAfter, err := e.st.GetInvoiceDocument(e.ctx, foreign.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(foreignAfter)
	if err != nil || string(encoded) != string(foreignBefore) {
		t.Fatal("rejected foreign selection changed the other invoice")
	}
}
