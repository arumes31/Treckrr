//go:build integration

package store_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

type invoiceSourceFixture struct {
	store                    *store.Store
	pool                     *sql.DB
	yearID, neighborID       int64
	unitEntryID, hourEntryID int64
	invoice                  models.Invoice
}

func newInvoiceSourceFixture(t *testing.T, taxMode string) invoiceSourceFixture {
	t.Helper()
	st, pool, yearID, neighborID, unitEntryID := invoiceFixture(t, true)
	ctx := t.Context()
	if err := st.UpdateCompany(ctx, models.Company{
		Name: "Synthetic source check farm", Address: "Synthetic farm road 1",
		TaxID: "ATU12345678", TaxMode: taxMode, VATRate: dec("13"),
		TaxNote: "Synthetic source check tax note",
	}); err != nil {
		t.Fatal(err)
	}
	hourEntryID, err := st.CreateEntry(ctx, &models.Entry{
		NeighborID: neighborID, BillingYearID: yearID, Date: day(2026, 2, 1),
		Hours: dec("1.25"), HourlyRate: dec("8"), Cost: dec("10"), Note: "Note fallback",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateEntry(ctx, &models.Entry{
		NeighborID: neighborID, BillingYearID: yearID, Date: day(2026, 2, 1),
		Unit: "Stück", Quantity: dec("3"), UnitPrice: dec("2.50"), Cost: dec("7.50"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	voidedID, err := st.CreateEntry(ctx, &models.Entry{
		NeighborID: neighborID, BillingYearID: yearID, Date: day(2026, 1, 1),
		TaskLabel: "Excluded voided work", Unit: "h", Hours: dec("2"),
		HourlyRate: dec("50"), Cost: dec("100"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetEntryVoided(ctx, voidedID, true, "Synthetic voided fixture"); err != nil {
		t.Fatal(err)
	}
	// Older rows can have an empty hour unit and a four-decimal stored cost.
	// The invoice stores its net in cents while retaining exact source lines.
	if _, err := pool.ExecContext(ctx, `UPDATE entries SET unit='',cost=12.3456 WHERE id=$1`, hourEntryID); err != nil {
		t.Fatal(err)
	}
	iv := issueFixtureInvoice(t, st, yearID, neighborID)
	if len(iv.Content.Lines) != 3 || iv.Content.Lines[0].Label != "Note fallback" ||
		iv.Content.Lines[1].Label != "Sonstige" || iv.Content.Lines[2].Label != "Review work" {
		t.Fatalf("fixture does not cover date/id ordering and label fallbacks: %+v", iv.Content.Lines)
	}
	if err := st.AddPayment(ctx, yearID, neighborID, dec("25"), day(2026, 9, 11), "Synthetic source check payment", "bank"); err != nil {
		t.Fatal(err)
	}
	return invoiceSourceFixture{store: st, pool: pool, yearID: yearID, neighborID: neighborID,
		unitEntryID: unitEntryID, hourEntryID: hourEntryID, invoice: iv}
}

func invoiceSourceState(t *testing.T, pool *sql.DB) map[string]string {
	t.Helper()
	state := make(map[string]string)
	for _, table := range []string{"invoices", "entries", "payments", "neighbor_ledger", "company", "neighbors", "audit_log"} {
		var digest string
		// Table names are a fixed test allowlist; invoice rows include content_hash.
		if err := pool.QueryRowContext(t.Context(),
			`SELECT md5(COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id)::text,'[]')) FROM `+table+` r`).Scan(&digest); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		state[table] = digest
	}
	return state
}

func checkInvoiceSourcesWithoutMutation(t *testing.T, f invoiceSourceFixture) store.InvoiceSourceCheck {
	t.Helper()
	before := invoiceSourceState(t, f.pool)
	report, err := f.store.CheckInvoiceSourceAssignments(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	after := invoiceSourceState(t, f.pool)
	for table, want := range before {
		if after[table] != want {
			t.Errorf("source check mutated %s: before=%s after=%s", table, want, after[table])
		}
	}
	return report
}

func TestInvoiceSourceCheckPristineIntegration(t *testing.T) {
	for _, taxMode := range []string{"regel", "pauschal", "kleinunternehmer"} {
		t.Run(taxMode, func(t *testing.T) {
			f := newInvoiceSourceFixture(t, taxMode)
			report := checkInvoiceSourcesWithoutMutation(t, f)
			if report.Checked != 1 || report.Matched != 1 || report.MatchedEntries != 3 || len(report.Issues) != 0 {
				t.Fatalf("pristine source check: %+v", report)
			}
		})
	}
}

func TestInvoiceSourceCheckMismatchesIntegration(t *testing.T) {
	for _, tt := range []struct {
		name, query, reason string
		hourEntry           bool
		invoice             bool
	}{
		{name: "missing snapshot", query: `UPDATE invoices SET net=NULL WHERE id=$1`, reason: "missing_snapshot", invoice: true},
		{name: "invalid snapshot", query: `UPDATE invoices SET lines='{}'::jsonb WHERE id=$1`, reason: "invalid_snapshot", invoice: true},
		{name: "source count", query: `UPDATE entries SET voided=true WHERE id=$1`, reason: "source_count_mismatch"},
		{name: "source label", query: `UPDATE entries SET task_label='Changed source label' WHERE id=$1`, reason: "source_line_mismatch"},
		{name: "source date", query: `UPDATE entries SET entry_date=entry_date+1 WHERE id=$1`, reason: "source_line_mismatch"},
		{name: "source unit", query: `UPDATE entries SET unit='Ballen' WHERE id=$1`, reason: "source_line_mismatch"},
		{name: "source quantity", query: `UPDATE entries SET quantity=2 WHERE id=$1`, reason: "source_line_mismatch"},
		{name: "source unit price", query: `UPDATE entries SET unit_price=101 WHERE id=$1`, reason: "source_line_mismatch"},
		{name: "source cost", query: `UPDATE entries SET cost=cost+0.01 WHERE id=$1`, reason: "source_line_mismatch"},
		{name: "source hours", query: `UPDATE entries SET hours=2 WHERE id=$1`, reason: "source_line_mismatch", hourEntry: true},
		{name: "source hourly rate", query: `UPDATE entries SET hourly_rate=9 WHERE id=$1`, reason: "source_line_mismatch", hourEntry: true},
		{name: "source total", query: `UPDATE invoices SET net=net+0.01 WHERE id=$1`, reason: "source_total_mismatch", invoice: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newInvoiceSourceFixture(t, "regel")
			id := f.unitEntryID
			if tt.hourEntry {
				id = f.hourEntryID
			}
			if tt.invoice {
				id = f.invoice.ID
			}
			// Corruption is injected only into this disposable fixture. Ordinary
			// application writes correctly reject changes to an issued account.
			if _, err := f.pool.ExecContext(t.Context(), tt.query, id); err != nil {
				t.Fatal(err)
			}
			report := checkInvoiceSourcesWithoutMutation(t, f)
			if report.Checked != 1 || report.Matched != 0 || report.MatchedEntries != 0 || len(report.Issues) != 1 {
				t.Fatalf("mismatch report: %+v", report)
			}
			issue := report.Issues[0]
			if issue.InvoiceID != f.invoice.ID || issue.YearID != f.yearID || issue.NeighborID != f.neighborID || issue.Reason != tt.reason {
				t.Fatalf("issue = %+v, want invoice=%d year=%d neighbor=%d reason=%s", issue, f.invoice.ID, f.yearID, f.neighborID, tt.reason)
			}
		})
	}
}

func TestInvoiceSourceCheckIgnoresMasterDataIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	if err := f.store.UpdateCompany(t.Context(), models.Company{
		Name: "Changed farm", Address: "Changed farm address", TaxMode: "kleinunternehmer",
		VATRate: dec("0"), TaxNote: "Changed tax note",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE neighbors SET name='Changed recipient',address='Changed address',tax_id='Changed tax ID' WHERE id=$1`, f.neighborID); err != nil {
		t.Fatal(err)
	}
	report := checkInvoiceSourcesWithoutMutation(t, f)
	if report.Checked != 1 || report.Matched != 1 || report.MatchedEntries != 3 || len(report.Issues) != 0 {
		t.Fatalf("master data edits changed source matching: %+v", report)
	}
}

func TestInvoiceSourceCheckComparesCalendarDaysIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	lines := append([]models.InvoiceLine(nil), f.invoice.Content.Lines...)
	lines[0].Date = time.Date(2026, 2, 1, 18, 30, 0, 0, time.FixedZone("fixture", 2*60*60))
	lines[0].Quantity = dec("1.25000")
	raw, err := json.Marshal(lines)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE invoices SET lines=$1 WHERE id=$2`, raw, f.invoice.ID); err != nil {
		t.Fatal(err)
	}
	report := checkInvoiceSourcesWithoutMutation(t, f)
	if report.Checked != 1 || report.Matched != 1 || report.MatchedEntries != 3 || len(report.Issues) != 0 {
		t.Fatalf("equivalent line representation failed source matching: %+v", report)
	}
}

func TestInvoiceSourceCheckIgnoresCanceledHistoryIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	if _, err := f.store.StornoInvoice(t.Context(), f.yearID, f.neighborID, "Synthetic source check cancellation"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE invoices SET net=NULL,lines=NULL WHERE id=$1`, f.invoice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE entries SET quantity=2 WHERE id=$1`, f.unitEntryID); err != nil {
		t.Fatal(err)
	}
	report := checkInvoiceSourcesWithoutMutation(t, f)
	if report.Checked != 0 || report.Matched != 0 || report.MatchedEntries != 0 || len(report.Issues) != 0 {
		t.Fatalf("canceled invoice or reversing document was checked: %+v", report)
	}
}

func TestInvoiceSourceCheckContinuesAfterIssueIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	neighborID, err := f.store.CreateNeighbor(t.Context(), "Second source check recipient", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE neighbors SET address='Second synthetic road' WHERE id=$1`, neighborID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddNeighborToYear(t.Context(), f.yearID, neighborID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateEntry(t.Context(), &models.Entry{
		NeighborID: neighborID, BillingYearID: f.yearID, Date: day(2026, 4, 1),
		TaskLabel: "Second account work", Unit: "h", Hours: dec("2"), HourlyRate: dec("15"), Cost: dec("30"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	issueFixtureInvoice(t, f.store, f.yearID, neighborID)
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE invoices SET net=NULL WHERE id=$1`, f.invoice.ID); err != nil {
		t.Fatal(err)
	}
	report := checkInvoiceSourcesWithoutMutation(t, f)
	if report.Checked != 2 || report.Matched != 1 || report.MatchedEntries != 1 || len(report.Issues) != 1 {
		t.Fatalf("mixed source check did not finish both accounts: %+v", report)
	}
	if report.Issues[0].InvoiceID != f.invoice.ID || report.Issues[0].Reason != "missing_snapshot" {
		t.Fatalf("mixed source check attributed the wrong issue: %+v", report.Issues)
	}
}
