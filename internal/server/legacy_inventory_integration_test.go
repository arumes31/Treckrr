//go:build integration

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

func TestLegacyInventoryInvoiceSourcesIntegration(t *testing.T) {
	e := newItEnv(t)
	e.post("/entries", url.Values{
		"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-04-03"},
		"hours": {"2"}, "unit": {"h"},
	})
	e.postIssue(e.neighborID, url.Values{"year_id": {itoa64(e.yearID64)}})
	if _, err := e.st.GetInvoice(e.ctx, e.yearID64, e.neighborID); err != nil {
		t.Fatalf("fixture invoice: %v", err)
	}
	financialSnapshot := func() string {
		t.Helper()
		var snapshot string
		err := e.pool.QueryRowContext(e.ctx, `SELECT jsonb_build_object(
			'entries', (SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM entries e WHERE billing_year_id=$1),
			'invoices', (SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM invoices i WHERE billing_year_id=$1),
			'payments', (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM payments p WHERE billing_year_id=$1),
			'ledger', (SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM neighbor_ledger l WHERE billing_year_id=$1))::text`, e.yearID64).Scan(&snapshot)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	get := func(path string) []byte {
		t.Helper()
		res, err := e.client.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("inventory response: status=%d cache=%q", res.StatusCode, res.Header.Get("Cache-Control"))
		}
		return body
	}
	before := financialSnapshot()
	var ordinary map[string]json.RawMessage
	if err := json.Unmarshal(get("/admin/legacy-inventory?format=json"), &ordinary); err != nil {
		t.Fatal(err)
	}
	if _, ok := ordinary["Entries"]; !ok {
		t.Fatal("ordinary inventory JSON changed shape")
	}
	if _, ok := ordinary["invoice_sources"]; ok {
		t.Fatal("ordinary inventory unexpectedly ran invoice-source reporting")
	}
	for range 2 {
		var report struct {
			Inventory      store.LegacyInventory    `json:"inventory"`
			InvoiceSources store.InvoiceSourceCheck `json:"invoice_sources"`
		}
		body := get("/admin/legacy-inventory?format=json&invoice_sources=1")
		if err := json.Unmarshal(body, &report); err != nil {
			t.Fatal(err)
		}
		if report.InvoiceSources.Checked < 1 || report.InvoiceSources.Matched < 1 || report.InvoiceSources.MatchedEntries < 1 {
			t.Fatalf("issued fixture not matched: %+v", report.InvoiceSources)
		}
		for _, private := range []string{e.uname, "IT-Betrieb", "Feldweg", "IT-Gespann"} {
			if strings.Contains(string(body), private) {
				t.Fatalf("report contains private source data %q", private)
			}
		}
	}
	page := string(get("/admin/legacy-inventory?invoice_sources=1"))
	for _, want := range []string{"Geprüfte Rechnungen", "Rechnungszuordnung prüfen", "schaltet mehrere Rechnungen pro Jahr nicht frei"} {
		if !strings.Contains(page, want) {
			t.Errorf("HTML report missing %q", want)
		}
	}
	if after := financialSnapshot(); before != after {
		t.Fatal("GET inventory changed financial rows or invoice snapshots")
	}
}
