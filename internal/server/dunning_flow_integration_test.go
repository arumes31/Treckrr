package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The dunning ladder went from stateless print links to recorded history,
// per-neighbor payment terms, fees, a grace deadline and a batch run
// (Ausbaukarte 32-36). This drives all of it through the real handlers.
func TestDunningFlowIntegration(t *testing.T) {
	e := newItEnv(t)
	nid, yid := e.neighborID, e.yearID64
	if _, err := e.pool.ExecContext(e.ctx, `UPDATE company SET payment_term_days=14`); err != nil {
		t.Fatal(err)
	}

	// A frozen invoice to dun: booking + issue (same path the lifecycle test uses).
	e.post("/entries", url.Values{
		"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-04-05"},
		"hours": {"2"}, "unit": {"h"},
	})
	e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})

	// Company: 14-day default term, 10-day grace, 5 € fee on the 1st Mahnung —
	// through the settings handler, so the new form fields are exercised too.
	e.post("/admin/company", url.Values{
		"name": {"IT-Betrieb"}, "address": {"Hofstraße 2, 4710 Testdorf"},
		"tax_id": {"ATU00000000"}, "tax_mode": {"pauschal"}, "vat_rate": {"13"},
		"payment_term_days": {"14"}, "dunning_grace_days": {"10"},
		"dunning_fee_1": {"5"}, "dunning_fee_2": {"12"},
	})

	// With the company's 14-day term the invoice (issued today) is NOT overdue.
	if page := e.get(fmt.Sprintf("/mahnwesen?year=%d", yid)); strings.Contains(page, "IT-Nachbar") {
		t.Fatalf("neighbor already dunned at a 14-day term on an invoice issued today")
	}

	// A later neighbor override must not rewrite newly frozen invoice terms.
	e.post(fmt.Sprintf("/neighbors/%d/update", nid), url.Values{
		"name": {"IT-Nachbar " + e.uname}, "note": {""},
		"address": {"Feldweg 1, 4710 Testdorf"}, "tax_id": {""}, "email": {""},
		"payment_term_days": {"0"},
	})
	page := e.get(fmt.Sprintf("/mahnwesen?year=%d", yid))
	if strings.Contains(page, "IT-Nachbar") {
		t.Fatal("later neighbor term rewrote frozen invoice terms")
	}
	// Historical NULL terms retain the established neighbor/company fallback.
	if _, err := e.pool.ExecContext(e.ctx, `UPDATE invoices SET payment_term_days=NULL WHERE billing_year_id=$1`, yid); err != nil {
		t.Fatal(err)
	}
	page = e.get(fmt.Sprintf("/mahnwesen?year=%d", yid))
	if !strings.Contains(page, "IT-Nachbar") {
		t.Fatalf("neighbor with a 0-day override is missing from the dunning list")
	}

	// Mark a 1st Mahnung as sent outside the app: history row + list line.
	e.post(fmt.Sprintf("/neighbors/%d/mahnung/mark-sent", nid), url.Values{
		"year": {itoa64(yid)}, "stufe": {"1"},
	})
	var stage int
	var channel string
	var fee string
	var grace time.Time
	if err := e.pool.QueryRowContext(e.ctx,
		`SELECT stage, channel, fee::text, grace_until FROM dunning_notices
		  WHERE billing_year_id=$1 AND neighbor_id=$2 ORDER BY sent_at DESC LIMIT 1`,
		yid, nid).Scan(&stage, &channel, &fee, &grace); err != nil {
		t.Fatalf("no dunning notice recorded: %v", err)
	}
	if stage != 1 || channel != "manuell" || fee != "0.00" {
		t.Errorf("notice = stage %d / %s / fee %s, want 1 / manuell / 0.00", stage, channel, fee)
	}
	wantGrace := time.Now().AddDate(0, 0, 10)
	if grace.Format("2006-01-02") != wantGrace.Format("2006-01-02") {
		t.Errorf("grace_until %s, want %s (today + 10)", grace.Format("2006-01-02"), wantGrace.Format("2006-01-02"))
	}
	if page := e.get(fmt.Sprintf("/mahnwesen?year=%d", yid)); !strings.Contains(page, "Zuletzt: 1. Mahnung") {
		t.Errorf("list does not show the recorded notice")
	}

	// Unbooked fee settings are retained and explained, never demanded.
	letter := e.get(fmt.Sprintf("/neighbors/%d/mahnung?year=%d&stufe=1", nid, yid))
	if !strings.Contains(letter, "nicht automatisch gebucht") || strings.Contains(letter, "inkl. 5,00") {
		t.Errorf("letter must explain why unbooked fees are not requested")
	}

	// Cross-year open items include the row and its year.
	if page := e.get(fmt.Sprintf("/mahnwesen?year=%d&scope=alle", yid)); !strings.Contains(page, "IT-Nachbar") {
		t.Errorf("all-years view is missing the overdue neighbor")
	}
}

// The batch run must not lose anyone: no address means skipped-and-said, a dead
// SMTP server means parked in the outbox — never a silent nothing.
// TestDunningBatchEmailFallsBackToOutboxIntegration verifies batch failures stay
// durable and repeated submissions remain idempotent.
func TestDunningBatchEmailFallsBackToOutboxIntegration(t *testing.T) {
	e := newItEnv(t)
	nid, yid := e.neighborID, e.yearID64
	e.post("/entries", url.Values{
		"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-04-06"},
		"hours": {"1"}, "unit": {"h"},
	})
	// Overdue immediately, and give the neighbor an address so the batch tries.
	e.post(fmt.Sprintf("/neighbors/%d/update", nid), url.Values{
		"name": {"IT-Nachbar " + e.uname}, "note": {""},
		"address": {"Feldweg 1, 4710 Testdorf"}, "tax_id": {""},
		"email": {"batch-" + e.uname + "@example.invalid"}, "payment_term_days": {"0"},
	})
	e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})

	e.post("/mahnwesen/batch-email", url.Values{"year": {itoa64(yid)}, "stufe": {"1"}})

	// SMTP points at a closed port (itEnv), so the mail must be parked — with the
	// stage's subject — and NO notice recorded (nothing was delivered).
	var kind, subject string
	if err := e.pool.QueryRowContext(e.ctx,
		`SELECT kind, subject FROM mail_outbox WHERE neighbor_id=$1 ORDER BY id DESC LIMIT 1`,
		nid).Scan(&kind, &subject); err != nil {
		t.Fatalf("batch failure did not reach the outbox: %v", err)
	}
	if kind != "mahnung" || !strings.Contains(subject, "1. Mahnung") {
		t.Errorf("outbox row = %s / %q, want mahnung / 1. Mahnung …", kind, subject)
	}
	var notices int
	if err := e.pool.QueryRowContext(e.ctx,
		`SELECT count(*) FROM dunning_notices WHERE neighbor_id=$1`, nid).Scan(&notices); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if notices != 0 {
		t.Errorf("%d notices recorded although nothing was delivered", notices)
	}

	// Repeating the normal batch action with identical document content must
	// reuse the pending intent instead of creating another deliverable message.
	e.post("/mahnwesen/batch-email", url.Values{"year": {itoa64(yid)}, "stufe": {"1"}})
	var intents int
	if err := e.pool.QueryRowContext(e.ctx,
		`SELECT count(*) FROM mail_outbox WHERE neighbor_id=$1 AND kind='mahnung'`, nid).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 1 {
		t.Errorf("repeated batch created %d intents, want 1", intents)
	}
}
