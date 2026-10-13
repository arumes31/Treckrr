//go:build integration

package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

func TestDunningBatchContinuesAfterInvalidSnapshotIntegration(t *testing.T) {
	e := newItEnv(t)
	var ids []int64
	for _, label := range []string{"A", "B", "C"} {
		id, err := e.st.CreateNeighbor(e.ctx, label+" "+e.uname, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if err := e.st.AddNeighborToYear(e.ctx, e.yearID64, id); err != nil {
			t.Fatal(err)
		}
		e.post(fmt.Sprintf("/neighbors/%d/update", id), url.Values{
			"name": {label + " " + e.uname}, "email": {"batch@example.invalid"}, "payment_term_days": {"0"},
			"address": {"Synthetic batch lane 1"},
		})
		// A and C are complete source-bound invoices. Corrupt only B's JSON
		// shape afterward: listing its amount must not abort the other sends.
		issuedOn := time.Now().AddDate(0, 0, -1)
		_, err = e.st.CreateEntry(e.ctx, &models.Entry{
			NeighborID: id, BillingYearID: e.yearID64, Date: issuedOn,
			TaskLabel: "Batch dunning", Unit: "h", Hours: decimal.NewFromInt(1),
			HourlyRate: decimal.NewFromInt(100), Cost: decimal.NewFromInt(100),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		invoice, err := e.st.IssueInvoice(e.ctx, e.yearID64, id, e.year, issuedOn)
		if err != nil {
			t.Fatal(err)
		}
		if label == "B" {
			if _, err := e.pool.ExecContext(e.ctx, `UPDATE invoices SET lines='{}'::jsonb WHERE id=$1`, invoice.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	body := e.post("/mahnwesen/batch-email", url.Values{"year": {itoa64(e.yearID64)}, "stufe": {"1"}})
	for _, want := range []string{"2 zur Wiederholung eingeplant", "1 fehlgeschlagen", "nur betroffene Nachbarn"} {
		if !strings.Contains(body, want) {
			t.Errorf("batch summary missing %q", want)
		}
	}
	for i, id := range ids {
		var count int
		if err := e.pool.QueryRowContext(e.ctx, `SELECT count(*) FROM mail_outbox WHERE neighbor_id=$1`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 1
		if i == 1 {
			want = 0
		}
		if count != want {
			t.Errorf("neighbor %d queued %d times, want %d", id, count, want)
		}
	}
}
