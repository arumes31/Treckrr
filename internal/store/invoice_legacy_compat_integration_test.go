//go:build integration

package store_test

import (
	"errors"
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

func TestLegacySnapshotsRemainUnchangedAfterAssignments(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(map[bool]string{true: "active fails closed", false: "canceled stays incomplete"}[active], func(t *testing.T) {
			st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
			ctx := t.Context()
			iv := issueFixtureInvoice(t, st, yearID, neighborID)
			if !active {
				if _, err := st.StornoInvoice(ctx, yearID, neighborID, "legacy fixture"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.ExecContext(ctx, `UPDATE invoices SET net=NULL,lines=NULL,content_hash='' WHERE id=$1`, iv.ID); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := pool.QueryRowContext(ctx, `SELECT to_jsonb(i)::text FROM invoices i WHERE id=$1`, iv.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			count, err := st.BackfillInvoiceSnapshots(ctx)
			if count != 0 || (active && !errors.Is(err, store.ErrInvoiceIncomplete)) || (!active && err != nil) {
				t.Fatalf("backfill active=%t: count=%d, error=%v", active, count, err)
			}
			docs, err := st.ListInvoiceDocs(ctx, yearID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, doc := range docs {
				if doc.ID == iv.ID {
					found = true
					if doc.Content != nil {
						t.Fatal("archive invented content from live annual bookings")
					}
				}
			}
			if !found {
				t.Fatal("archive hid the incomplete historical document")
			}
			err = st.ReconcileAfterRestore(ctx)
			if (active && !errors.Is(err, store.ErrInvoiceIncomplete)) || (!active && err != nil) {
				t.Fatalf("restore reconciliation active=%t: %v", active, err)
			}
			var after string
			if err := pool.QueryRowContext(ctx, `SELECT to_jsonb(i)::text FROM invoices i WHERE id=$1`, iv.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("startup/restore compatibility changed historical invoice")
			}
		})
	}
}

// TestLegacyPreflightWithoutAssignmentTable simulates an old schema only in the
// owned disposable database. No ordinary runtime reader needs a fallback.
func TestLegacyPreflightWithoutAssignmentTable(t *testing.T) {
	st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
	ctx := t.Context()
	iv := issueFixtureInvoice(t, st, yearID, neighborID)
	if _, err := pool.ExecContext(ctx, `ALTER TABLE invoice_entries RENAME TO invoice_entries_preflight_hidden`); err != nil {
		t.Fatal(err)
	}
	report, err := st.CheckInvoiceSourceAssignments(ctx)
	if err != nil || report.Checked != 1 || report.Matched != 1 || len(report.Issues) != 0 {
		t.Fatalf("pre-migration preflight: %+v, %v", report, err)
	}
	if _, err := pool.ExecContext(ctx, `UPDATE invoices SET net=NULL,lines=NULL,content_hash='' WHERE id=$1`, iv.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := st.BackfillInvoiceSnapshots(ctx); err != nil || count != 1 {
		t.Fatalf("legacy backfill = %d, %v", count, err)
	}
	filled, err := st.GetInvoice(ctx, yearID, neighborID)
	if err != nil || filled.Content == nil || !filled.Content.Gross.Equal(iv.Content.Gross) || filled.Content.Hash != iv.Content.Hash {
		t.Fatalf("legacy backfill changed substance: %+v, %v", filled, err)
	}
	if count, err := st.BackfillInvoiceSnapshots(ctx); err != nil || count != 0 {
		t.Fatalf("legacy backfill retry = %d, %v", count, err)
	}
}

func TestInvoiceSourceCheckUsesDocumentAssignments(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	ctx := t.Context()
	if _, err := f.store.CreateEntry(ctx, flatEntry(f.yearID, f.neighborID, 2026, "50"), nil); err != nil {
		t.Fatal(err)
	}
	report := checkInvoiceSourcesWithoutMutation(t, f)
	if report.Checked != 1 || report.Matched != 1 || report.MatchedEntries != 3 || len(report.Issues) != 0 {
		t.Fatalf("pending work falsely changed frozen source match: %+v", report)
	}
	issueFixtureInvoice(t, f.store, f.yearID, f.neighborID)
	report = checkInvoiceSourcesWithoutMutation(t, f)
	if report.Checked != 2 || report.Matched != 2 || report.MatchedEntries != 4 || len(report.Issues) != 0 {
		t.Fatalf("multiple invoice assignments: %+v", report)
	}
	if _, err := f.pool.ExecContext(ctx, `UPDATE invoice_entries SET ordinal=ordinal+100 WHERE invoice_id=$1`, f.invoice.ID); err != nil {
		t.Fatal(err)
	}
	report = checkInvoiceSourcesWithoutMutation(t, f)
	if len(report.Issues) != 1 || report.Issues[0].InvoiceID != f.invoice.ID || report.Issues[0].Reason != "source_assignment_mismatch" {
		t.Fatalf("invalid ordinal provenance not reported: %+v", report)
	}
}
