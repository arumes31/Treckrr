//go:build integration

package server

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/d0linger/treckrr/internal/store"
)

// TestRequestInvoiceSelectionOwnershipIntegration exercises the uncalled
// selection helper against the current single-invoice schema. It intentionally
// does not relax any booking lock or enable multiple active invoices.
func TestRequestInvoiceSelectionOwnershipIntegration(t *testing.T) {
	e := newItEnv(t)
	nid, yid := e.neighborID, e.yearID64
	e.post("/entries", url.Values{
		"year_id": {itoa64(yid)}, "neighbor_id": {itoa64(nid)},
		"gespann_id": {itoa64(e.gespannID)}, "entry_date": {"2026-03-01"},
		"hours": {"2"}, "unit": {"h"},
	})
	e.post(fmt.Sprintf("/neighbors/%d/anzahlung", nid), url.Values{
		"year_id": {itoa64(yid)}, "amount": {"10"}, "label": {"Selection fixture"},
	})
	advances, err := e.st.ListAnzahlungen(e.ctx, yid, nid)
	if err != nil || len(advances) != 1 {
		t.Fatalf("advance fixture: count=%d, err=%v", len(advances), err)
	}
	e.postIssue(nid, url.Values{"year_id": {itoa64(yid)}})
	invoice, err := e.st.GetInvoice(e.ctx, yid, nid)
	if err != nil || invoice.Content == nil {
		t.Fatalf("invoice fixture: %v", err)
	}
	s := &Server{store: e.st}
	for _, tc := range []struct {
		name, query    string
		year, neighbor int64
		wantErr        error
	}{
		{name: "legacy sole active invoice", year: yid, neighbor: nid},
		{name: "selected own invoice", query: "invoice_id=" + itoa64(invoice.ID), year: yid, neighbor: nid},
		{name: "different neighbor", query: "invoice_id=" + itoa64(invoice.ID), year: yid, neighbor: nid + 1, wantErr: store.ErrNotFound},
		{name: "different year", query: "invoice_id=" + itoa64(invoice.ID), year: yid + 1, neighbor: nid, wantErr: store.ErrNotFound},
		{name: "missing document", query: "invoice_id=9223372036854775807", year: yid, neighbor: nid, wantErr: store.ErrNotFound},
		{name: "advance is not invoice", query: "invoice_id=" + itoa64(advances[0].ID), year: yid, neighbor: nid, wantErr: store.ErrNotFound},
		{name: "malformed must not fall back", query: "invoice_id=invalid", year: yid, neighbor: nid, wantErr: errInvoiceSelection},
		{name: "duplicate must not fall back", query: "invoice_id=1&invoice_id=2", year: yid, neighbor: nid, wantErr: errInvoiceSelection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?"+tc.query, nil)
			got, err := s.requestInvoice(r, tc.year, tc.neighbor, true)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("selection error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (got.ID != invoice.ID || got.Content.Hash != invoice.Content.Hash) {
				t.Fatal("selection did not preserve the frozen document identity")
			}
			if tc.wantErr != nil && got.ID != 0 {
				t.Fatal("invalid selection returned an invoice")
			}
		})
	}
	e.post(fmt.Sprintf("/neighbors/%d/invoice/storno", nid), url.Values{
		"year_id": {itoa64(yid)}, "reason": {"Selection fixture correction"},
	})
	query := "/?invoice_id=" + itoa64(invoice.ID)
	if _, err := s.requestInvoice(httptest.NewRequest("GET", query, nil), yid, nid, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("canceled invoice accepted as active: %v", err)
	}
	historical, err := s.requestInvoice(httptest.NewRequest("GET", query, nil), yid, nid, false)
	if err != nil || historical.Content == nil || historical.Content.Hash != invoice.Content.Hash || historical.Status != "canceled" {
		t.Fatalf("historical frozen invoice changed: id=%d status=%q err=%v", historical.ID, historical.Status, err)
	}
}
