//go:build integration

package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

// TestSingleInvoiceBookingGuardsIntegration freezes existing invoice sources and
// evidence while allowing new work in the same open account.
func TestSingleInvoiceBookingGuardsIntegration(t *testing.T) {
	e := newItEnv(t)
	nid, yid := e.neighborID, e.yearID64
	personID, err := e.st.CreatePerson(e.ctx, "Guard helper "+e.uname, decimal.NewFromInt(20), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := e.pool.ExecContext(e.ctx, `DELETE FROM persons WHERE id=$1`, personID); err != nil {
			t.Errorf("remove owned synthetic person: %v", err)
		}
	})
	e.post("/entries", url.Values{
		"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-02-01"},
		"hours": {"1"}, "unit": {"h"}, "person_id": {itoa64(personID)},
	})
	sources, err := e.st.ListEntries(e.ctx, nid, yid)
	if err != nil || len(sources) != 2 {
		t.Fatalf("paired sources: %v, count=%d", err, len(sources))
	}
	var mainID, helperID int64
	for _, source := range sources {
		if source.LinkedEntryID == nil {
			mainID = source.ID
		} else {
			helperID = source.ID
		}
	}
	if mainID == 0 || helperID == 0 {
		t.Fatal("fixture did not produce both linked sources")
	}
	photoID, err := e.st.AddEntryPhoto(e.ctx, mainID, []byte("synthetic guard evidence"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})
	e.post(fmt.Sprintf("/neighbors/%d/payments", nid), url.Values{
		"year_id": {itoa64(yid)}, "amount": {"10"}, "paid_on": {"2026-02-02"},
		"method": {"bar"}, "note": {"Synthetic guard prepayment"},
	})
	invoice, err := e.st.GetInvoice(e.ctx, yid, nid)
	if err != nil || invoice.Content == nil {
		t.Fatalf("fixture invoice: %v", err)
	}
	remaining, err := e.st.AccountRemaining(e.ctx, yid, nid)
	if err != nil {
		t.Fatal(err)
	}
	before := invoiceGuardSnapshot(t, e)
	for _, path := range []string{
		neighborBookingURL(nid, yid),
		fmt.Sprintf("/entries/%d/copy", mainID),
	} {
		if page := e.get(path); strings.Contains(page, "data-booking-locked") {
			t.Errorf("new work is unexpectedly locked on %s", path)
		}
	}
	for _, path := range []string{
		fmt.Sprintf("/entries/%d/edit", mainID),
		fmt.Sprintf("/entries/%d/edit", helperID),
	} {
		if page := e.get(path); !strings.Contains(page, "data-booking-locked") {
			t.Errorf("frozen source lock not visible on %s", path)
		}
	}
	for _, tc := range []struct {
		name, path string
		form       url.Values
	}{
		{
			name: "edit machine and synchronize companion", path: fmt.Sprintf("/entries/%d/update", mainID),
			form: url.Values{"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-02-01"},
				"hours": {"8"}, "unit": {"h"}, "sync_pair": {"1"}},
		},
		{
			name: "edit companion", path: fmt.Sprintf("/entries/%d/update", helperID),
			form: url.Values{"unit": {models.UnitMannstunde}, "quantity": {"8"}, "unit_price": {"20"}},
		},
		{
			name: "void group", path: fmt.Sprintf("/entries/%d/void", mainID),
			form: url.Values{"voided": {"true"}, "cascade": {"1"}, "reason": {"guard test"}},
		},
		{
			name: "delete group", path: fmt.Sprintf("/entries/%d/delete", mainID),
			form: url.Values{"cascade": {"1"}},
		},
		{
			name: "delete companion", path: fmt.Sprintf("/entries/%d/delete", helperID),
			form: url.Values{},
		},
		{
			name: "delete evidence", path: fmt.Sprintf("/entries/%d/photos/%d/delete", mainID, photoID),
			form: url.Values{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subEnv := *e
			subEnv.t = t
			page := subEnv.post(tc.path, tc.form)
			if !strings.Contains(page, "festgeschrieben") && !strings.Contains(page, "gesperrt") {
				t.Error("blocked mutation did not explain the invoice lock")
			}
			if after := invoiceGuardSnapshot(t, e); after != before {
				t.Fatal("blocked mutation changed sources, evidence, invoice, payment or ledger")
			}
		})
	}
	preserved, err := e.st.GetInvoiceDocument(e.ctx, invoice.ID)
	if err != nil || preserved.Content == nil || preserved.Content.Hash != invoice.Content.Hash {
		t.Fatalf("issued document no longer resolves unchanged: %v", err)
	}
	if after, err := e.st.AccountRemaining(e.ctx, yid, nid); err != nil || !after.Equal(remaining) {
		t.Fatalf("blocked mutations changed balance: %v, got=%s want=%s", err, after, remaining)
	}
	// Repricing has no pending sources yet, so it must be a harmless no-op.
	e.post(fmt.Sprintf("/neighbors/%d/recalc", nid), url.Values{"year_id": {itoa64(yid)}})
	if after := invoiceGuardSnapshot(t, e); after != before {
		t.Fatal("repricing rewrote frozen sources")
	}
	// Only newly created rows may change; preserve the original pair, invoice,
	// payment and evidence byte-for-byte while accepting both work directions.
	frozen := func() string {
		var value string
		err := e.pool.QueryRowContext(e.ctx, `SELECT jsonb_build_object(
			'entries',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM entries x WHERE id IN ($1,$2)),
			'invoice',(SELECT to_jsonb(x) FROM invoices x WHERE id=$3),
			'payments',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM payments x WHERE billing_year_id=$4 AND neighbor_id=$5),
			'photos',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM entry_photos x WHERE entry_id IN ($1,$2)))::text`,
			mainID, helperID, invoice.ID, yid, nid).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	original := frozen()
	e.post("/entries", url.Values{"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-03-01"}, "hours": {"1"}, "unit": {"h"}})
	newEntries, err := e.st.ListEntries(e.ctx, nid, yid)
	if err != nil || len(newEntries) != 3 {
		t.Fatalf("new outgoing work after invoice: count=%d, err=%v", len(newEntries), err)
	}
	e.post("/entries", url.Values{"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"booking_form_version": {"2"}, "booking_kind": {"equipment"}, "booking_direction": {"in"},
		"mode": {"gespann"}, "gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-03-01"}, "hours": {"1"}})
	var incoming int
	if err := e.pool.QueryRowContext(e.ctx, `SELECT count(*) FROM neighbor_ledger
		WHERE billing_year_id=$1 AND neighbor_id=$2 AND amount<0 AND NOT voided`, yid, nid).Scan(&incoming); err != nil || incoming != 1 {
		t.Fatalf("new incoming work after invoice: count=%d, err=%v", incoming, err)
	}
	if frozen() != original {
		t.Fatal("new work changed existing invoice sources, payment or evidence")
	}

	// Year completion remains an independent lock, including an uninvoiced member.
	unbilled := e.secondNeighbor("Unbilled guard")
	beforeClose := invoiceGuardSnapshot(t, e)
	if err := e.st.SetYearStatus(e.ctx, yid, models.YearCompleted); err != nil {
		t.Fatal(err)
	}
	e.book(unbilled, "2026-04-01")
	closed, err := e.st.ListEntries(e.ctx, unbilled, yid)
	if err != nil || len(closed) != 0 {
		t.Fatalf("closed year accepted uninvoiced work: %v, count=%d", err, len(closed))
	}
	if after := invoiceGuardSnapshot(t, e); after != beforeClose {
		t.Fatal("closed-year request changed the invoiced account")
	}
}

// invoiceGuardSnapshot includes full financial and evidence rows so a rejected
// request cannot silently change metadata while preserving only its total.
func invoiceGuardSnapshot(t *testing.T, e *itEnv) string {
	t.Helper()
	var snapshot string
	err := e.pool.QueryRowContext(e.ctx, `SELECT jsonb_build_object(
		'entries', (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM entries x WHERE billing_year_id=$1 AND neighbor_id=$2),
		'invoices', (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM invoices x WHERE billing_year_id=$1 AND neighbor_id=$2),
		'payments', (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM payments x WHERE billing_year_id=$1 AND neighbor_id=$2),
		'ledger', (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM neighbor_ledger x WHERE billing_year_id=$1 AND neighbor_id=$2),
		'photos', (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM entry_photos p
		 JOIN entries e ON e.id=p.entry_id WHERE e.billing_year_id=$1 AND e.neighbor_id=$2))::text`,
		e.yearID64, e.neighborID,
	).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
