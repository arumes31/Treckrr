//go:build integration

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

type multiAllocationFixture struct {
	store              *store.Store
	pool               *sql.DB
	yearID, neighborID int64
	first, second      models.Invoice
}

func newMultiAllocationFixture(t *testing.T) multiAllocationFixture {
	t.Helper()
	st, pool, yearID, neighborID, _ := invoiceFixture(t, false)
	first := issueFixtureInvoice(t, st, yearID, neighborID)
	addMultiAllocationWork(t, st, yearID, neighborID, "200")
	second := issueFixtureInvoice(t, st, yearID, neighborID)
	if first.ID == second.ID || !first.Content.Gross.Equal(dec("100")) || !second.Content.Gross.Equal(dec("200")) {
		t.Fatal("fixture did not issue two independently frozen invoices")
	}
	return multiAllocationFixture{store: st, pool: pool, yearID: yearID, neighborID: neighborID, first: first, second: second}
}

func addMultiAllocationWork(t *testing.T, st *store.Store, yearID, neighborID int64, amount string) int64 {
	t.Helper()
	id, err := st.CreateEntry(t.Context(), &models.Entry{
		NeighborID: neighborID, BillingYearID: yearID, Date: day(2026, 9, 10),
		TaskLabel: "Synthetic subsequent work", Unit: "Pauschale",
		Quantity: dec("1"), UnitPrice: dec(amount), Cost: dec(amount),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertMultiAllocation(t *testing.T, f multiAllocationFixture, want map[int64]string, annual string) store.InvoiceAllocationSummary {
	t.Helper()
	before := invoiceSourceState(t, f.pool)
	summary, err := f.store.AccountInvoiceAllocations(t.Context(), f.yearID, f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Invoices) != len(want) {
		t.Fatalf("allocation documents=%d want=%d", len(summary.Invoices), len(want))
	}
	for _, invoice := range summary.Invoices {
		expected, ok := want[invoice.InvoiceID]
		if !ok || !invoice.Remaining.Equal(dec(expected)) {
			t.Fatalf("invoice %d remaining=%s want=%q", invoice.InvoiceID, invoice.Remaining, expected)
		}
		got, err := f.store.InvoiceDocumentRemaining(t.Context(), invoice.InvoiceID)
		if err != nil || !got.Equal(invoice.Remaining) {
			t.Fatalf("selected remaining=%s err=%v", got, err)
		}
	}
	remaining, err := f.store.AccountRemaining(t.Context(), f.yearID, f.neighborID)
	if err != nil || !remaining.Equal(dec(annual)) {
		t.Fatalf("annual remaining=%s want=%s err=%v", remaining, annual, err)
	}
	legacy, err := f.store.InvoiceRemaining(t.Context(), f.yearID, f.neighborID)
	if err != nil || !legacy.Equal(remaining) {
		t.Fatalf("legacy annual wrapper=%s want=%s err=%v", legacy, remaining, err)
	}
	neighbors, err := f.store.YearNeighborSummaries(t.Context(), f.yearID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, neighbor := range neighbors {
		if neighbor.NeighborID == f.neighborID {
			found = neighbor.Remaining.Equal(remaining)
		}
	}
	if !found {
		t.Fatalf("dashboard summary=%+v missing expected account balance", neighbors)
	}
	history, err := f.store.NeighborYearHistory(t.Context(), f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, row := range history {
		if row.YearID == f.yearID {
			found = row.Remaining.Equal(remaining)
		}
	}
	if !found {
		t.Fatalf("history=%+v missing expected year balance", history)
	}
	_, open, credit, err := f.store.YearPaymentTotals(t.Context(), f.yearID)
	if err != nil || !open.Equal(decimal.Max(remaining, decimal.Zero)) || !credit.Equal(decimal.Max(remaining.Neg(), decimal.Zero)) {
		t.Fatalf("year open=%s credit=%s annual=%s err=%v", open, credit, remaining, err)
	}
	all, err := f.store.AllYearPaymentTotals(t.Context())
	if err != nil || !all[f.yearID].Open.Equal(open) || !all[f.yearID].Credit.Equal(credit) {
		t.Fatalf("all-year totals=%+v err=%v", all, err)
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(before, after) {
		t.Fatal("allocation reads changed financial or audit rows")
	}
	return summary
}

func TestMultiInvoiceAllocationFIFOAndPendingIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	before := invoiceSourceState(t, f.pool)["invoices"]
	addMultiAllocationWork(t, f.store, f.yearID, f.neighborID, "50")
	if err := f.store.AddPayment(t.Context(), f.yearID, f.neighborID, dec("150"), day(2026, 9, 11), "", "bank"); err != nil {
		t.Fatal(err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "0", f.second.ID: "150"}, "200")
	if after := invoiceSourceState(t, f.pool)["invoices"]; after != before {
		t.Fatal("subsequent work or FIFO payment changed frozen invoices")
	}
	rows, err := f.store.DunningRows(t.Context(), f.yearID, 14, day(2026, 11, 1))
	if err != nil || len(rows) != 1 || rows[0].InvoiceID != f.second.ID || !rows[0].Open.Equal(dec("150")) {
		t.Fatalf("per-document dunning=%+v err=%v", rows, err)
	}
	checks, err := f.store.YearClosingChecks(t.Context(), f.yearID)
	if err != nil {
		t.Fatal(err)
	}
	foundPending, foundUnpaid := false, false
	for _, check := range checks {
		if check.Key == "uninvoiced" {
			foundPending = check.Count == 1
		}
		if check.Key == "unpaid" {
			foundUnpaid = check.Count == 1 && check.Amount.Equal(dec("150"))
		}
	}
	if !foundPending || !foundUnpaid {
		t.Fatalf("closing checks lost pending work or per-document debt: %+v", checks)
	}
	if err := f.store.SetYearStatus(t.Context(), f.yearID, "completed"); err != nil {
		t.Fatal(err)
	}
	paid, err := f.store.SettleRemainingWithMethod(t.Context(), f.yearID, f.neighborID, day(2026, 11, 2), "Synthetic settlement", "bar")
	if err != nil || !paid.Equal(dec("200")) {
		t.Fatalf("closed-year settlement=%s err=%v", paid, err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "0", f.second.ID: "0"}, "0")
	if paid, err := f.store.SettleRemaining(t.Context(), f.yearID, f.neighborID, day(2026, 11, 2), "repeat"); err != nil || !paid.IsZero() {
		t.Fatalf("duplicate settlement=%s err=%v", paid, err)
	}
}

func TestMultiInvoiceAllocationExplicitSurplusAndAdvanceIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	payment, err := f.store.RecordPayment(t.Context(), store.PaymentInput{
		YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
		Amount: dec("250"), PaidOn: day(2026, 9, 11), IdempotencyKey: "synthetic-explicit",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "50", f.second.ID: "0"}, "50")
	if err := f.store.AddPayment(t.Context(), f.yearID, f.neighborID, dec("95.67"), day(2026, 9, 12), "", "bank"); err != nil {
		t.Fatal(err)
	}
	summary := assertMultiAllocation(t, f, map[int64]string{f.first.ID: "0", f.second.ID: "0"}, "-45.67")
	if !summary.UnallocatedCash.Equal(dec("45.67")) || !summary.AccountResidual.Equal(dec("-45.67")) {
		t.Fatalf("surplus was lost or counted twice: %+v", summary)
	}
	var before string
	if err := f.pool.QueryRowContext(t.Context(), `SELECT to_jsonb(p)::text FROM payments p WHERE id=$1`, payment.PaymentID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	addMultiAllocationWork(t, f.store, f.yearID, f.neighborID, "60")
	third := issueFixtureInvoice(t, f.store, f.yearID, f.neighborID)
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "0", f.second.ID: "0", third.ID: "14.33"}, "14.33")
	var after string
	if err := f.pool.QueryRowContext(t.Context(), `SELECT to_jsonb(p)::text FROM payments p WHERE id=$1`, payment.PaymentID).Scan(&after); err != nil || before != after {
		t.Fatalf("later invoice rewrote existing cash: err=%v", err)
	}
}

func TestMultiInvoiceAllocationSelectionAtomicityIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	other, err := f.store.CreateNeighbor(t.Context(), "Synthetic foreign selection", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddNeighborToYear(t.Context(), f.yearID, other); err != nil {
		t.Fatal(err)
	}
	baseID, err := f.store.CreateEmptyBase(t.Context(), 2027, "Synthetic selection year")
	if err != nil {
		t.Fatal(err)
	}
	otherYear, err := f.store.CreateBillingYear(t.Context(), 2027, baseID, "Synthetic selection year")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddNeighborToYear(t.Context(), otherYear, f.neighborID); err != nil {
		t.Fatal(err)
	}
	input := store.PaymentInput{YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
		Amount: dec("50"), PaidOn: day(2026, 9, 11), IdempotencyKey: "synthetic-selection"}
	first, err := f.store.RecordPayment(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	state := invoiceSourceState(t, f.pool)
	replay, err := f.store.RecordPayment(t.Context(), input)
	if err != nil || !replay.Duplicate || replay.PaymentID != first.PaymentID {
		t.Fatalf("payment replay=%+v err=%v", replay, err)
	}
	foreign := input
	foreign.NeighborID, foreign.IdempotencyKey = other, "synthetic-foreign-neighbor"
	if _, err := f.store.RecordPayment(t.Context(), foreign); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign account invoice accepted: %v", err)
	}
	foreign = input
	foreign.YearID, foreign.IdempotencyKey = otherYear, "synthetic-foreign-year"
	if _, err := f.store.RecordPayment(t.Context(), foreign); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign year invoice accepted: %v", err)
	}
	input.InvoiceID = f.first.ID
	if _, err := f.store.RecordPayment(t.Context(), input); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed payment preference accepted: %v", err)
	}
	input.IdempotencyKey, input.InvoiceID, input.SkontoPct = "synthetic-ambiguous-skonto", 0, dec("2")
	if _, err := f.store.RecordPayment(t.Context(), input); !errors.Is(err, store.ErrInvoiceAmbiguous) {
		t.Fatalf("unselected multi-invoice Skonto accepted: %v", err)
	}
	input.InvoiceID = 9999999
	if _, err := f.store.RecordPayment(t.Context(), input); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing selected invoice accepted: %v", err)
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(state, after) {
		t.Fatal("replay/refused selection wrote cash, a credit or audit rows")
	}
	input.InvoiceID, input.IdempotencyKey, input.SkontoPct = f.second.ID, "synthetic-excess-skonto", dec("110")
	if _, err := f.store.RecordPayment(t.Context(), input); !errors.Is(err, store.ErrGutschriftTooLarge) {
		t.Fatalf("excess selected Skonto accepted: %v", err)
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(state, after) {
		t.Fatal("refused credit cap wrote cash, a credit or audit rows")
	}
	input.InvoiceID, input.IdempotencyKey, input.SkontoPct = f.second.ID, "synthetic-selected-skonto", dec("2")
	result, err := f.store.RecordPayment(t.Context(), input)
	if err != nil || result.Skonto == nil || result.Skonto.ReferencesInvoiceID == nil ||
		*result.Skonto.ReferencesInvoiceID != f.second.ID || !result.Skonto.Content.Gross.Equal(dec("-4")) {
		t.Fatalf("selected Skonto=%+v err=%v", result, err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "100", f.second.ID: "96"}, "196")
	if err := f.store.SetYearStatus(t.Context(), f.yearID, "completed"); err != nil {
		t.Fatal(err)
	}
	state = invoiceSourceState(t, f.pool)
	input.IdempotencyKey = "synthetic-closed-skonto"
	if _, err := f.store.RecordPayment(t.Context(), input); !errors.Is(err, store.ErrSkontoYearCompleted) {
		t.Fatalf("closed-year Skonto accepted: %v", err)
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(state, after) {
		t.Fatal("closed-year credit refusal wrote cash, credit or audit rows")
	}
}

func TestMultiInvoiceAllocationReversalIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	hash := strings.Repeat("a", 64)
	_, rows, err := f.store.CreatePaymentImportBatch(t.Context(), 0, strings.Repeat("b", 64), []store.PaymentImportRowInput{{
		RowNo: 1, TransactionHash: hash, TransactionDate: day(2026, 9, 11), Amount: dec("100"),
		YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.first.ID, Status: store.PaymentImportPending,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if booked, err := f.store.ImportPayment(t.Context(), rows[0], hash, f.yearID, f.neighborID, f.first.ID, dec("100"), day(2026, 9, 11), "Synthetic import"); err != nil || !booked {
		t.Fatalf("import booked=%t err=%v", booked, err)
	}
	if _, err := f.store.RecordPayment(t.Context(), store.PaymentInput{
		YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
		Amount: dec("50"), PaidOn: day(2026, 9, 12),
	}); err != nil {
		t.Fatal(err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "0", f.second.ID: "150"}, "150")
	var original string
	if err := f.pool.QueryRowContext(t.Context(), `SELECT to_jsonb(p)::text FROM payments p
		JOIN payment_import_rows r ON r.payment_id=p.id WHERE r.id=$1`, rows[0]).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.ReverseImportedPayment(t.Context(), rows[0]); err != nil {
		t.Fatal(err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "100", f.second.ID: "150"}, "250")
	state := invoiceSourceState(t, f.pool)
	if _, _, err := f.store.ReverseImportedPayment(t.Context(), rows[0]); !errors.Is(err, store.ErrImportAlreadyReversed) {
		t.Fatalf("duplicate reversal err=%v", err)
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(state, after) {
		t.Fatal("duplicate reversal changed financial state")
	}
	var preserved string
	if err := f.pool.QueryRowContext(t.Context(), `SELECT to_jsonb(p)::text FROM payments p
		JOIN payment_import_rows r ON r.payment_id=p.id WHERE r.id=$1`, rows[0]).Scan(&preserved); err != nil || preserved != original {
		t.Fatalf("reversal changed imported payment: %v", err)
	}
}

func TestMultiInvoiceAllocationCreditsAndPositiveLedgerIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	if _, err := f.store.GutschriftInvoiceByID(t.Context(), f.yearID, f.neighborID, f.second.ID, dec("200"), "Synthetic full credit"); err != nil {
		t.Fatal(err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "100", f.second.ID: "0"}, "100")
	if _, err := f.store.FreeGutschrift(t.Context(), f.yearID, f.neighborID, 2026, dec("20"), "Synthetic standalone"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AddNeighborLedger(t.Context(), f.yearID, f.neighborID, dec("-30"), "Synthetic offset", day(2026, 10, 12)); err != nil {
		t.Fatal(err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "50", f.second.ID: "0"}, "50")
	if _, err := f.store.AddNeighborLedger(t.Context(), f.yearID, f.neighborID, dec("70"), "Synthetic debit", day(2026, 10, 12)); err != nil {
		t.Fatal(err)
	}
	// Annual debt includes the positive net ledger40; document remaining does
	// not. This distinction must never silently change the legacy annual API.
	summary := assertMultiAllocation(t, f, map[int64]string{f.first.ID: "80", f.second.ID: "0"}, "120")
	if !summary.AccountResidual.Equal(dec("40")) {
		t.Fatalf("positive ledger residual=%s want40", summary.AccountResidual)
	}
}

func TestMultiInvoiceAllocationCanceledPreferenceAndResourcesIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	payment, err := f.store.RecordPayment(t.Context(), store.PaymentInput{
		YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
		Amount: dec("50"), PaidOn: day(2026, 9, 11),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.StornoInvoiceByID(t.Context(), f.yearID, f.neighborID, f.second.ID, "Synthetic cancellation"); err != nil {
		t.Fatal(err)
	}
	// B's released work remains pending200, but its selected cash is now
	// available to A without erasing the original explicit preference.
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "50"}, "250")
	var preferred, historical int64
	if err := f.pool.QueryRowContext(t.Context(), `SELECT preferred_invoice_id,invoice_id FROM payments WHERE id=$1`,
		payment.PaymentID).Scan(&preferred, &historical); err != nil || preferred != f.second.ID || historical != f.second.ID {
		t.Fatalf("cancellation rewrote preference/history=%d/%d err=%v", preferred, historical, err)
	}
	if _, err := f.store.InvoiceDocumentRemaining(t.Context(), f.second.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("canceled document remaining err=%v", err)
	}
	// One connection belongs to the fixture's company lock. Reads must fully
	// release each result before starting the next query.
	f.pool.SetMaxOpenConns(2)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.store.AccountInvoiceAllocations(ctx, f.yearID, f.neighborID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled allocation read err=%v", err)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "50"}, "250")
}

func TestMultiInvoiceAllocationConcurrentPaymentReplayIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type outcome struct {
		result store.PaymentResult
		err    error
	}
	results := make(chan outcome, 8)
	start := make(chan struct{})
	for range 8 {
		go func() {
			<-start
			result, err := f.store.RecordPayment(ctx, store.PaymentInput{
				YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
				Amount: dec("50"), PaidOn: day(2026, 9, 11), IdempotencyKey: "synthetic-concurrent",
			})
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)
	var paymentID int64
	created := 0
	for range 8 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if paymentID != 0 && result.result.PaymentID != paymentID {
				t.Fatal("concurrent replay created different payment IDs")
			}
			paymentID = result.result.PaymentID
			if !result.result.Duplicate {
				created++
			}
		case <-ctx.Done():
			t.Fatal("concurrent payment replay did not finish")
		}
	}
	if created != 1 {
		t.Fatalf("new payments=%d want1", created)
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "100", f.second.ID: "150"}, "250")
}

func TestMultiInvoiceAllocationDunningAndDeliveryIdentityIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	ctx := t.Context()
	if _, err := f.pool.ExecContext(ctx, `UPDATE neighbors SET payment_term_days=30 WHERE id=$1`, f.neighborID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.store.DunningRows(ctx, f.yearID, 14, day(2026, 10, 1))
	if err != nil || len(rows) != 0 {
		t.Fatalf("neighbor term override was not preserved: %+v %v", rows, err)
	}
	rows, err = f.store.DunningRows(ctx, f.yearID, 14, day(2026, 11, 1))
	if err != nil || len(rows) != 2 || rows[0].InvoiceID != f.first.ID || rows[1].InvoiceID != f.second.ID {
		t.Fatalf("distinct document dunning: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.TermDays != 30 || !row.DueOn.Equal(day(2026, 10, 10)) {
			t.Fatalf("due date lost legacy neighbor/company fallback: %+v", row)
		}
	}
	for _, notice := range []store.DunningNotice{
		{InvoiceNumber: f.first.Number, Stage: 0},
		{InvoiceNumber: f.second.Number, Stage: 1},
		{InvoiceNumber: f.first.Number, Stage: 2},
	} {
		notice.BillingYearID, notice.NeighborID, notice.Channel = f.yearID, f.neighborID, "manuell"
		if err := f.store.RecordDunningNotice(ctx, notice); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []int64{0, f.yearID} {
		latest, err := f.store.LastDunningNoticesByInvoice(ctx, scope)
		if err != nil || len(latest) != 2 || latest[f.first.ID] == nil || latest[f.second.ID] == nil ||
			latest[f.first.ID].Stage != 2 || latest[f.second.ID].Stage != 1 {
			t.Fatalf("reminders crossed document identity: %+v %v", latest, err)
		}
	}
	assertUnsent := func(want int) {
		t.Helper()
		checks, err := f.store.YearClosingChecks(ctx, f.yearID)
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range checks {
			if check.Key == "unsent" {
				if check.Count != want {
					t.Fatalf("unsent accounts=%d want=%d", check.Count, want)
				}
				return
			}
		}
		t.Fatal("missing unsent check")
	}
	// Ambiguous annual history is retained, but never assigned to both documents.
	if err := f.store.RecordBelegSend(ctx, f.yearID, f.neighborID, "print"); err != nil {
		t.Fatal(err)
	}
	assertUnsent(1)
	for i, invoice := range []models.Invoice{f.first, f.second} {
		if _, err := f.pool.ExecContext(ctx, `INSERT INTO beleg_sends
			(billing_year_id,neighbor_id,invoice_id,channel) VALUES($1,$2,$3,'print')`,
			f.yearID, f.neighborID, invoice.ID); err != nil {
			t.Fatal(err)
		}
		assertUnsent(1 - i)
	}
}
