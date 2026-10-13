//go:build integration

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

// TestInvoiceIssueConcurrentYearCloseIntegration forces both commit orders,
// including a second invoice already present before the competing requests.
func TestInvoiceIssueConcurrentYearCloseIntegration(t *testing.T) {
	for _, first := range []string{"issue", "close"} {
		t.Run(first+" first", func(t *testing.T) {
			f := newMultiAllocationFixture(t)
			pendingID := addMultiAllocationWork(t, f.store, f.yearID, f.neighborID, "50")
			preview, err := f.store.BuildPendingInvoiceContent(t.Context(), f.yearID, f.neighborID)
			if err != nil {
				t.Fatal(err)
			}
			before := retainedBillingRows(t, f.pool)
			var issued models.Invoice
			issue := func(ctx context.Context) error {
				var issueErr error
				issued, issueErr = f.store.IssueInvoiceConfirmed(
					ctx, f.yearID, f.neighborID, 2026, day(2026, 9, 10), preview.Hash,
				)
				return issueErr
			}
			closeYear := func(ctx context.Context) error {
				return f.store.SetYearStatus(ctx, f.yearID, "completed")
			}
			calls := []func(context.Context) error{issue, closeYear}
			issueIndex, closeIndex := 0, 1
			if first == "close" {
				calls = []func(context.Context) error{closeYear, issue}
				issueIndex, closeIndex = 1, 0
			}
			results := runOrderedInvoiceCalls(t, f, calls)
			if results[closeIndex] != nil {
				t.Fatalf("year close: %v", results[closeIndex])
			}
			want := map[int64]string{f.first.ID: "100", f.second.ID: "200"}
			wantClaims := 2
			if first == "close" {
				if !errors.Is(results[issueIndex], store.ErrYearCompleted) || issued.ID != 0 {
					t.Fatalf("issue after close = %d/%v, want refusal", issued.ID, results[issueIndex])
				}
				pending, err := f.store.BuildPendingInvoiceContent(t.Context(), f.yearID, f.neighborID)
				if err != nil || pending.Hash != preview.Hash {
					t.Fatalf("refused issuance changed pending work: %v", err)
				}
			} else {
				if results[issueIndex] != nil || issued.ID == 0 {
					t.Fatalf("issue before close = %d/%v", issued.ID, results[issueIndex])
				}
				want[issued.ID], wantClaims = "50", 3
				_, err := f.store.BuildPendingInvoiceContent(t.Context(), f.yearID, f.neighborID)
				if !errors.Is(err, store.ErrNoPendingEntries) {
					t.Fatalf("issued work remains pending: %v", err)
				}
			}
			year, err := f.store.GetBillingYear(t.Context(), f.yearID)
			if err != nil || !year.Completed() {
				t.Fatalf("completed year = %+v/%v", year, err)
			}
			entry, err := f.store.GetEntry(t.Context(), pendingID)
			if err != nil || entry.InvoiceLocked != (first == "issue") {
				t.Fatalf("pending source claim disagrees with winning operation: %+v/%v", entry, err)
			}
			var claims, cash int
			if err := f.pool.QueryRowContext(t.Context(), `SELECT
				(SELECT count(*) FROM invoice_entries WHERE active),
				(SELECT count(*) FROM payments)`).Scan(&claims, &cash); err != nil {
				t.Fatal(err)
			}
			if claims != wantClaims || cash != 0 {
				t.Fatalf("claims/cash = %d/%d, want %d/0", claims, cash, wantClaims)
			}
			assertMultiAllocation(t, f, want, "350")
			assertRetainedBillingRows(t, f.pool, before)
		})
	}
}

// TestInvoiceSelectedPaymentConcurrentCorrectionIntegration preserves exact
// selection and cash when cancellation or a large credit races a payment.
func TestInvoiceSelectedPaymentConcurrentCorrectionIntegration(t *testing.T) {
	for _, correction := range []string{"storno", "credit"} {
		for _, first := range []string{"payment", "correction"} {
			t.Run(correction+"/"+first+" first", func(t *testing.T) {
				f := newMultiAllocationFixture(t)
				if err := f.store.AddPayment(
					t.Context(), f.yearID, f.neighborID, dec("10"), day(2026, 9, 11), "Existing cash", "bank",
				); err != nil {
					t.Fatal(err)
				}
				before := retainedBillingRows(t, f.pool)
				input := store.PaymentInput{
					YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
					Amount: dec("50"), PaidOn: day(2026, 9, 12), IdempotencyKey: "racing-selected-payment",
				}
				var payment store.PaymentResult
				pay := func(ctx context.Context) error {
					var paymentErr error
					payment, paymentErr = f.store.RecordPayment(ctx, input)
					return paymentErr
				}
				var adjustment models.Invoice
				correct := func(ctx context.Context) error {
					var correctionErr error
					if correction == "storno" {
						adjustment, correctionErr = f.store.StornoInvoiceByID(
							ctx, f.yearID, f.neighborID, f.second.ID, "Synthetic concurrent correction",
						)
					} else {
						adjustment, correctionErr = f.store.GutschriftInvoiceByID(
							ctx, f.yearID, f.neighborID, f.second.ID, dec("180"), "Synthetic concurrent credit",
						)
					}
					return correctionErr
				}
				calls := []func(context.Context) error{pay, correct}
				paymentIndex, correctionIndex := 0, 1
				if first == "correction" {
					calls = []func(context.Context) error{correct, pay}
					paymentIndex, correctionIndex = 1, 0
				}
				results := runOrderedInvoiceCalls(t, f, calls)
				if results[correctionIndex] != nil || adjustment.ID == 0 {
					t.Fatalf("correction = %d/%v", adjustment.ID, results[correctionIndex])
				}
				isRejected := correction == "storno" && first == "correction"
				wantPayments, wantPaid := 2, "60"
				if isRejected {
					wantPayments, wantPaid = 1, "10"
					if !errors.Is(results[paymentIndex], store.ErrNotFound) || payment.PaymentID != 0 {
						t.Fatalf("payment for canceled invoice = %+v/%v", payment, results[paymentIndex])
					}
				} else {
					if results[paymentIndex] != nil || payment.PaymentID == 0 || payment.Duplicate {
						t.Fatalf("selected payment = %+v/%v", payment, results[paymentIndex])
					}
					replay, err := f.store.RecordPayment(t.Context(), input)
					if err != nil || !replay.Duplicate || replay.PaymentID != payment.PaymentID {
						t.Fatalf("post-correction replay = %+v/%v", replay, err)
					}
					var preferred, historical int64
					if err := f.pool.QueryRowContext(t.Context(),
						`SELECT preferred_invoice_id,invoice_id FROM payments WHERE id=$1`, payment.PaymentID,
					).Scan(&preferred, &historical); err != nil {
						t.Fatal(err)
					}
					if preferred != f.second.ID || historical != f.second.ID {
						t.Fatalf("payment selection/history = %d/%d, want %d", preferred, historical, f.second.ID)
					}
				}
				payments, err := f.store.ListPayments(t.Context(), f.yearID, f.neighborID)
				if err != nil || len(payments) != wantPayments {
					t.Fatalf("cash rows = %d/%v, want %d", len(payments), err, wantPayments)
				}
				paid, err := f.store.NeighborPaymentSum(t.Context(), f.yearID, f.neighborID)
				if err != nil || !paid.Equal(dec(wantPaid)) {
					t.Fatalf("cash total = %s/%v, want %s", paid, err, wantPaid)
				}
				want := map[int64]string{f.first.ID: "60", f.second.ID: "0"}
				annual := "60"
				if correction == "storno" {
					want, annual = map[int64]string{f.first.ID: "40"}, "240"
					if isRejected {
						want, annual = map[int64]string{f.first.ID: "90"}, "290"
					}
					pending, err := f.store.BuildPendingInvoiceContent(t.Context(), f.yearID, f.neighborID)
					if err != nil || len(pending.Lines) != 1 || !pending.Net.Equal(dec("200")) {
						t.Fatalf("storno released incorrect work: %+v/%v", pending, err)
					}
				} else if !adjustment.Content.Gross.Equal(dec("-180")) {
					t.Fatalf("credit amount = %s, want -180", adjustment.Content.Gross)
				}
				assertMultiAllocation(t, f, want, annual)
				assertRetainedBillingRows(t, f.pool, before)
			})
		}
	}
}

// TestMultiInvoiceConcurrentSettlementIntegration proves that double-clicks and
// competing carry/settle actions cannot duplicate a mixed annual balance.
func TestMultiInvoiceConcurrentSettlementIntegration(t *testing.T) {
	for _, mode := range []string{"settle", "carry", "settle then carry", "carry then settle"} {
		t.Run(mode, func(t *testing.T) {
			f, targetYear := multiInvoiceSettlementFixture(t)
			before := retainedBillingRows(t, f.pool)
			// Six blocked workers fit the regular pool, alongside the company's
			// pinned connection, the lock holder and the lock-monitor query.
			const workers = 6
			amounts := make([]decimal.Decimal, workers)
			kinds := make([]string, workers)
			calls := make([]func(context.Context) error, workers)
			for i := range workers {
				kind := mode
				if mode == "settle then carry" || mode == "carry then settle" {
					kind = "carry"
					if (mode == "settle then carry" && i == 0) || (mode == "carry then settle" && i > 0) {
						kind = "settle"
					}
				}
				kinds[i] = kind
				calls[i] = func(ctx context.Context) error {
					var err error
					if kind == "settle" {
						amounts[i], err = f.store.SettleRemainingWithMethod(
							ctx, f.yearID, f.neighborID, day(2026, 10, 1), "Concurrent final payment", "bank",
						)
					} else {
						amounts[i], err = f.store.CarryForwardRemaining(
							ctx, f.neighborID, f.yearID, targetYear, day(2026, 10, 1),
							"Concurrent carry out", "Concurrent carry in",
						)
					}
					return err
				}
			}
			results := runOrderedInvoiceCalls(t, f, calls)
			for i, err := range results {
				if err != nil {
					t.Fatalf("request %d (%s): %v", i, kinds[i], err)
				}
				want := decimal.Zero
				if i == 0 {
					want = dec("290")
				}
				if !amounts[i].Equal(want) {
					t.Fatalf("request %d (%s) moved %s, want %s", i, kinds[i], amounts[i], want)
				}
			}
			assertMultiAllocation(t, f, map[int64]string{f.first.ID: "0", f.second.ID: "0"}, "0")
			wantPayments, wantPaid, wantTarget, wantTransfers := 3, "370", "0", 0
			if kinds[0] == "carry" {
				wantPayments, wantPaid, wantTarget, wantTransfers = 2, "80", "290", 2
			}
			payments, err := f.store.ListPayments(t.Context(), f.yearID, f.neighborID)
			if err != nil || len(payments) != wantPayments {
				t.Fatalf("payments = %d/%v, want %d", len(payments), err, wantPayments)
			}
			paid, err := f.store.NeighborPaymentSum(t.Context(), f.yearID, f.neighborID)
			if err != nil || !paid.Equal(dec(wantPaid)) {
				t.Fatalf("paid = %s/%v, want %s", paid, err, wantPaid)
			}
			target, err := f.store.AccountRemaining(t.Context(), targetYear, f.neighborID)
			if err != nil || !target.Equal(dec(wantTarget)) {
				t.Fatalf("target remainder = %s/%v, want %s", target, err, wantTarget)
			}
			var transfers, transferIDs int
			var transferTotal decimal.Decimal
			if err := f.pool.QueryRowContext(t.Context(), `SELECT count(*),count(DISTINCT transfer_id),
				COALESCE(sum(amount),0) FROM neighbor_ledger WHERE transfer_id <> ''`,
			).Scan(&transfers, &transferIDs, &transferTotal); err != nil {
				t.Fatal(err)
			}
			if transfers != wantTransfers || transferIDs != wantTransfers/2 || !transferTotal.IsZero() {
				t.Fatalf("carry rows/ids/net = %d/%d/%s, want %d/%d/0",
					transfers, transferIDs, transferTotal, wantTransfers, wantTransfers/2)
			}
			assertRetainedBillingRows(t, f.pool, before)
		})
	}
}

// multiInvoiceSettlementFixture includes amounts outside invoice allocations so
// the concurrency test cannot pass by accidentally settling only the documents.
func multiInvoiceSettlementFixture(t *testing.T) (multiAllocationFixture, int64) {
	t.Helper()
	f := newMultiAllocationFixture(t)
	addMultiAllocationWork(t, f.store, f.yearID, f.neighborID, "50")
	if _, err := f.store.AddNeighborLedger(
		t.Context(), f.yearID, f.neighborID, dec("20"), "Existing annual debit", day(2026, 9, 11),
	); err != nil {
		t.Fatal(err)
	}
	for _, input := range []store.PaymentInput{
		{YearID: f.yearID, NeighborID: f.neighborID, Amount: dec("30"), PaidOn: day(2026, 9, 11)},
		{YearID: f.yearID, NeighborID: f.neighborID, InvoiceID: f.second.ID,
			Amount: dec("50"), PaidOn: day(2026, 9, 12)},
	} {
		if _, err := f.store.RecordPayment(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	assertMultiAllocation(t, f, map[int64]string{f.first.ID: "70", f.second.ID: "150"}, "290")
	year, err := f.store.GetBillingYear(t.Context(), f.yearID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.store.CreateBillingYear(t.Context(), 2027, year.BaseID, "Synthetic concurrent carry target")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddNeighborToYear(t.Context(), target, f.neighborID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetYearStatus(t.Context(), f.yearID, "completed"); err != nil {
		t.Fatal(err)
	}
	return f, target
}

// runOrderedInvoiceCalls establishes real PostgreSQL lock waits before release,
// instead of relying on goroutine scheduling or sleeps to produce a race. Each
// successive exclusive waiter is observed before the next request is started.
func runOrderedInvoiceCalls(
	t *testing.T,
	f multiAllocationFixture,
	calls []func(context.Context) error,
) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	holder, err := f.pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback() //nolint:errcheck // no-op after the explicit commit
	var holderPID int
	if err := holder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, f.yearID); err != nil {
		t.Fatal(err)
	}
	done := make([]chan error, len(calls))
	for i, call := range calls {
		done[i] = make(chan error, 1)
		go func() { done[i] <- call(ctx) }()
		waitForLockWaiters(t, ctx, f.pool, holderPID, i+1)
	}
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	results := make([]error, len(calls))
	for i, result := range done {
		select {
		case results[i] = <-result:
		case <-ctx.Done():
			t.Fatalf("concurrent request %d did not finish: %v", i, ctx.Err())
		}
	}
	return results
}

// retainedBillingRows captures every stored column of existing financial rows.
// Invoice status is checked through active allocations because cancellation is
// intentional; all other document content, metadata and timestamps stay exact.
func retainedBillingRows(t *testing.T, pool *sql.DB) map[string]string {
	t.Helper()
	rows, err := pool.QueryContext(t.Context(), `
		SELECT 'invoice:'||id,to_jsonb(i)-'status' FROM invoices i
		UNION ALL SELECT 'entry:'||id,to_jsonb(e) FROM entries e
		UNION ALL SELECT 'payment:'||id,to_jsonb(p) FROM payments p
		UNION ALL SELECT 'ledger:'||id,to_jsonb(l) FROM neighbor_ledger l`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertRetainedBillingRows permits new postings, never rewriting old money.
func assertRetainedBillingRows(t *testing.T, pool *sql.DB, before map[string]string) {
	t.Helper()
	after := retainedBillingRows(t, pool)
	for key, want := range before {
		if got, exists := after[key]; !exists || got != want {
			t.Errorf("existing %s changed: got %s, want %s", key, got, want)
		}
	}
}
