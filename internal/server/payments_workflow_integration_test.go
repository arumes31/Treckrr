//go:build integration

package server

import (
	"html"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestPaymentWorkflowOpenYearIntegration(t *testing.T) {
	e := newItEnv(t)
	if _, err := e.st.AddNeighborLedger(e.ctx, e.yearID64, e.neighborID, decimal.NewFromInt(100), "Opening claim", time.Now()); err != nil {
		t.Fatal(err)
	}
	page := html.UnescapeString(e.get(dashboardURL(e.yearID64)))
	for _, want := range []string{"Zahlung verbuchen", `value="credit"`, `value="settled"`, "Leistungssaldo"} {
		if !strings.Contains(page, want) {
			t.Errorf("open-year dashboard missing %q", want)
		}
	}
	formPage := e.get(neighborPaymentsURL(e.neighborID, e.yearID64))
	if !strings.Contains(formPage, `value="100.00"`) || !strings.Contains(formPage, "Gesamten Rest bezahlen") {
		t.Fatal("payment form has no prefilled remainder/full settlement")
	}
	e.post("/neighbors/"+itoa64(e.neighborID)+"/payments", url.Values{
		"year_id": {itoa64(e.yearID64)}, "amount": {"25"}, "paid_on": {"2026-10-01"}, "method": {"bar"}, "idempotency_key": {"workflow-partial"},
	})
	// Replay is harmless, and full settlement recomputes the changed remainder.
	e.post("/neighbors/"+itoa64(e.neighborID)+"/payments", url.Values{
		"year_id": {itoa64(e.yearID64)}, "amount": {"25"}, "paid_on": {"2026-10-01"}, "method": {"bar"}, "idempotency_key": {"workflow-partial"},
	})
	values := url.Values{"year_id": {itoa64(e.yearID64)}, "neighbor_id": {itoa64(e.neighborID)}, "origin": {"payments"}, "paid_on": {"2026-10-02"}, "method": {"überweisung"}, "amount": {"100"}, "note": {"Bank transfer"}}
	e.post("/years/mark-paid", values)
	e.post("/years/mark-paid", values)
	payments, err := e.st.ListPayments(e.ctx, e.yearID64, e.neighborID)
	if err != nil || len(payments) != 2 {
		t.Fatalf("payments = %d, %v; want two", len(payments), err)
	}
	var sum decimal.Decimal
	var full bool
	for _, p := range payments {
		sum = sum.Add(p.Amount)
		if p.Amount.Equal(decimal.NewFromInt(75)) {
			full = p.Method == "überweisung" && p.PaidOn.Format("2006-01-02") == "2026-10-02" && p.Note == "Bank transfer"
		}
	}
	if !sum.Equal(decimal.NewFromInt(100)) || !full {
		t.Fatalf("sum=%s, full settlement date/method=%v", sum, full)
	}
	values.Set("paid_on", "not-a-date")
	if page := e.post("/years/mark-paid", values); !strings.Contains(page, "gültiges Zahlungsdatum") {
		t.Fatal("invalid settlement date accepted")
	}
}
