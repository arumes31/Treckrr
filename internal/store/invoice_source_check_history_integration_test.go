//go:build integration

package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/d0linger/treckrr/internal/models"
)

// This pins the legacy cancellation/reissue lifecycle before source assignments
// are introduced. A readiness check must not rebind or rewrite historical data.
func TestInvoiceSourceCheckPreservesReissuedHistoryIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	ctx := t.Context()
	credit, err := f.store.GutschriftInvoice(ctx, f.yearID, f.neighborID, dec("10"), "Synthetic correction")
	if err != nil {
		t.Fatal(err)
	}
	storno, err := f.store.StornoInvoice(ctx, f.yearID, f.neighborID, "Synthetic replacement")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateEntry(ctx, &models.Entry{
		NeighborID: f.neighborID, BillingYearID: f.yearID, Date: day(2026, 9, 12),
		TaskLabel: "Replacement work", Unit: "h", Hours: dec("1"),
		HourlyRate: dec("20"), Cost: dec("20"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	replacement, err := f.store.IssueInvoice(ctx, f.yearID, f.neighborID, 2026, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == f.invoice.ID || replacement.Number == f.invoice.Number {
		t.Fatal("replacement reused historical invoice identity")
	}
	if _, err := f.store.CreateBelegShare(
		ctx, "synthetic-source-history-token-hash", f.neighborID, f.yearID,
		time.Now().Add(time.Hour), "synthetic-test",
	); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetYearStatus(ctx, f.yearID, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddPayment(
		ctx, f.yearID, f.neighborID, dec("5"), time.Now(), "Payment after closing", "cash",
	); err != nil {
		t.Fatal(err)
	}
	wantRemaining := replacement.Content.Gross.Sub(dec("30"))
	beforeRemaining, err := f.store.AccountRemaining(ctx, f.yearID, f.neighborID)
	if err != nil || !beforeRemaining.Equal(wantRemaining) {
		t.Fatalf("account remaining=%s (%v), want %s", beforeRemaining, err, wantRemaining)
	}
	var beforeShares string
	if err := f.pool.QueryRowContext(ctx,
		`SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY id)::text,'[]') FROM beleg_shares s`,
	).Scan(&beforeShares); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		report := checkInvoiceSourcesWithoutMutation(t, f)
		if report.Checked != 1 || report.Matched != 1 || report.MatchedEntries != 4 || len(report.Issues) != 0 {
			t.Fatalf("replacement check included canceled invoice/credit/reversal: %+v", report)
		}
	}
	var afterShares string
	if err := f.pool.QueryRowContext(ctx,
		`SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY id)::text,'[]') FROM beleg_shares s`,
	).Scan(&afterShares); err != nil {
		t.Fatal(err)
	}
	if beforeShares != afterShares {
		t.Fatal("readiness check changed public share records")
	}
	for _, want := range []models.Invoice{f.invoice, credit, storno, replacement} {
		got, err := f.store.GetInvoiceDocument(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, err := json.Marshal(want.Content)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := json.Marshal(got.Content)
		if err != nil {
			t.Fatal(err)
		}
		if got.Number != want.Number || !got.IssuedOn.Equal(want.IssuedOn) || string(gotJSON) != string(wantJSON) {
			t.Fatalf("historical document %d changed identity, date or frozen content", want.ID)
		}
	}
	afterRemaining, err := f.store.AccountRemaining(ctx, f.yearID, f.neighborID)
	if err != nil || !afterRemaining.Equal(beforeRemaining) {
		t.Fatalf("readiness check changed remaining balance: %s -> %s (%v)", beforeRemaining, afterRemaining, err)
	}
}
