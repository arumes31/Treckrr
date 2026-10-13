//go:build integration

package store_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

// TestInvoiceSourceLifecycle keeps the document and source identity contract
// together: concurrent confirmation, later identical work, selective cancellation
// and reissuance all run through the real store in an owned disposable database.
func TestInvoiceSourceLifecycle(t *testing.T) {
	st, pool, yearID, neighborID, firstEntryID := invoiceFixture(t, true)
	ctx := t.Context()
	preview, err := st.BuildPendingInvoiceContent(ctx, yearID, neighborID)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		invoice models.Invoice
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			invoice, err := st.IssueInvoiceConfirmed(ctx, yearID, neighborID, 2026, time.Time{}, preview.Hash)
			results <- result{invoice: invoice, err: err}
		}()
	}
	close(start)
	first, duplicate := <-results, <-results
	if first.err != nil || duplicate.err != nil || first.invoice.ID != duplicate.invoice.ID {
		t.Fatalf("concurrent confirmation: first=%d/%v second=%d/%v",
			first.invoice.ID, first.err, duplicate.invoice.ID, duplicate.err)
	}
	snapshot := func(invoiceID int64) string {
		t.Helper()
		var body string
		if err := pool.QueryRowContext(ctx,
			`SELECT to_jsonb(i)::text FROM invoices i WHERE id=$1`, invoiceID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	frozenFirst := snapshot(first.invoice.ID)
	var sourceBefore string
	if err := pool.QueryRowContext(ctx, `SELECT to_jsonb(e)::text FROM entries e WHERE id=$1`,
		firstEntryID).Scan(&sourceBefore); err != nil {
		t.Fatal(err)
	}
	entry, err := st.GetEntry(ctx, firstEntryID)
	if err != nil {
		t.Fatal(err)
	}
	later := *entry
	later.ID, later.InvoiceLocked = 0, false
	laterID, err := st.CreateEntry(ctx, &later, nil)
	if err != nil {
		t.Fatalf("book after first invoice: %v", err)
	}
	next, err := st.BuildPendingInvoiceContent(ctx, yearID, neighborID)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Lines) != 1 || next.Hash == preview.Hash {
		t.Fatalf("identical subsequent work must have its own source-bound preview: %+v", next)
	}
	replay, err := st.IssueInvoiceConfirmed(ctx, yearID, neighborID, 2026, time.Time{}, preview.Hash)
	if err != nil || replay.ID != first.invoice.ID {
		t.Fatalf("old confirmation replay must return old invoice: %d %v", replay.ID, err)
	}
	second, err := st.IssueInvoiceConfirmed(ctx, yearID, neighborID, 2026, time.Time{}, next.Hash)
	if err != nil || second.ID == first.invoice.ID {
		t.Fatalf("second invoice: %d %v", second.ID, err)
	}
	frozenSecond := snapshot(second.ID)
	if snapshot(first.invoice.ID) != frozenFirst {
		t.Fatal("subsequent booking or invoice changed first frozen document")
	}
	var sourceAfter string
	if err := pool.QueryRowContext(ctx, `SELECT to_jsonb(e)::text FROM entries e WHERE id=$1`,
		firstEntryID).Scan(&sourceAfter); err != nil {
		t.Fatal(err)
	}
	if sourceAfter != sourceBefore {
		t.Fatal("subsequent issuance changed the first booking")
	}
	var claims string
	if err := pool.QueryRowContext(ctx, `
		SELECT string_agg(invoice_id::text||':'||entry_id::text||':'||source_entry_id::text,',' ORDER BY invoice_id)
		  FROM invoice_entries WHERE active AND invoice_id IN ($1,$2)`,
		first.invoice.ID, second.ID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	wantClaims := fmt.Sprintf("%d:%d:%d,%d:%d:%d",
		first.invoice.ID, firstEntryID, firstEntryID, second.ID, laterID, laterID)
	if claims != wantClaims {
		t.Fatalf("source ownership = %s, want %s", claims, wantClaims)
	}
	if _, err := st.GetInvoice(ctx, yearID, neighborID); !errors.Is(err, store.ErrInvoiceAmbiguous) {
		t.Fatalf("unselected lookup must reject ambiguity: %v", err)
	}
	if _, err := st.BuildPendingInvoiceContent(ctx, yearID, neighborID); !errors.Is(err, store.ErrNoPendingEntries) {
		t.Fatalf("fully invoiced account still has pending work: %v", err)
	}
	annual, err := st.AccountRemaining(ctx, yearID, neighborID)
	want := first.invoice.Content.Gross.Add(second.Content.Gross)
	if err != nil || !annual.Equal(want) {
		t.Fatalf("annual total = %s/%v, want %s", annual, err, want)
	}
	if _, err := st.StornoInvoice(ctx, yearID, neighborID, "ambiguous"); !errors.Is(err, store.ErrInvoiceAmbiguous) {
		t.Fatalf("unselected cancellation must reject ambiguity: %v", err)
	}
	if snapshot(first.invoice.ID) != frozenFirst || snapshot(second.ID) != frozenSecond {
		t.Fatal("rejected cancellation changed a document")
	}
	if _, err := st.StornoInvoiceByID(ctx, yearID, neighborID, first.invoice.ID, "Synthetic correction"); err != nil {
		t.Fatal(err)
	}
	if snapshot(second.ID) != frozenSecond {
		t.Fatal("canceling first invoice changed second invoice")
	}
	correction, err := st.BuildPendingInvoiceContent(ctx, yearID, neighborID)
	if err != nil || len(correction.Lines) != 1 {
		t.Fatalf("released first source only: lines=%d error=%v", len(correction.Lines), err)
	}
	third, err := st.IssueInvoiceConfirmed(ctx, yearID, neighborID, 2026, time.Time{}, correction.Hash)
	if err != nil || third.ID == first.invoice.ID || third.ID == second.ID {
		t.Fatalf("reissue released sources: %d %v", third.ID, err)
	}
	annual, err = st.AccountRemaining(ctx, yearID, neighborID)
	if err != nil || !annual.Equal(want) {
		t.Fatalf("reissued annual total = %s/%v, want unchanged %s", annual, err, want)
	}
	if snapshot(second.ID) != frozenSecond {
		t.Fatal("reissuing first sources changed second document")
	}
}

// TestInvoiceSourceOwnershipRejectsForeignSelection proves that selection never
// changes the account boundary even when a valid invoice ID belongs elsewhere.
func TestInvoiceSourceOwnershipRejectsForeignSelection(t *testing.T) {
	st, _, yearID, neighborID, _ := invoiceFixture(t, false)
	invoice := issueFixtureInvoice(t, st, yearID, neighborID)
	other, err := st.CreateNeighbor(t.Context(), "Other synthetic owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddNeighborToYear(t.Context(), yearID, other); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StornoInvoiceByID(t.Context(), yearID, other, invoice.ID, "invalid"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign cancellation = %v", err)
	}
	if _, err := st.GutschriftInvoiceByID(t.Context(), yearID, other, invoice.ID,
		invoice.Content.Gross, "invalid"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign credit = %v", err)
	}
	after, err := st.GetInvoiceDocument(t.Context(), invoice.ID)
	if err != nil || after.Status != "issued" || after.Content.Hash != invoice.Content.Hash {
		t.Fatalf("foreign selection changed original: %+v %v", after, err)
	}
}

// TestInvoiceIssueReplayAfterClosing preserves no-op retries without opening
// the closed year for new documents or altering any financial history.
func TestInvoiceIssueReplayAfterClosing(t *testing.T) {
	st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
	preview, err := st.BuildPendingInvoiceContent(t.Context(), yearID, neighborID)
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := st.IssueInvoiceConfirmed(t.Context(), yearID, neighborID, 2026, time.Time{}, preview.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetYearStatus(t.Context(), yearID, "completed"); err != nil {
		t.Fatal(err)
	}
	before := invoiceSourceState(t, pool)
	for _, hash := range []string{preview.Hash, ""} {
		got, err := st.IssueInvoiceConfirmed(t.Context(), yearID, neighborID, 2026, time.Time{}, hash)
		if hash == "" {
			got, err = st.IssueInvoice(t.Context(), yearID, neighborID, 2026, time.Time{})
		}
		if err != nil || got.ID != invoice.ID {
			t.Fatalf("closed replay = %d/%v", got.ID, err)
		}
	}
	if after := invoiceSourceState(t, pool); !reflect.DeepEqual(before, after) {
		t.Fatal("closed-year no-op replay changed financial history")
	}
}
