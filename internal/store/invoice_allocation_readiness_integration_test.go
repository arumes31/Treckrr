//go:build integration

package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/d0linger/treckrr/internal/store"
)

func allocationReadinessWithoutMutation(t *testing.T, f invoiceSourceFixture) store.InvoiceAllocationReadiness {
	t.Helper()
	before := invoiceSourceState(t, f.pool)
	remaining, err := f.store.AccountRemaining(t.Context(), f.yearID, f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.store.CheckInvoiceAllocationReadiness(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(before, after) {
		t.Fatal("allocation readiness changed persisted financial or audit state")
	}
	afterRemaining, err := f.store.AccountRemaining(t.Context(), f.yearID, f.neighborID)
	if err != nil || !afterRemaining.Equal(remaining) {
		t.Fatalf("allocation readiness changed current balance: before=%s after=%s err=%v", remaining, afterRemaining, err)
	}
	return report
}

func TestInvoiceAllocationReadinessPristineIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	if _, err := f.store.GutschriftInvoice(t.Context(), f.yearID, f.neighborID, dec("10"), "Synthetic credit"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AddNeighborLedger(t.Context(), f.yearID, f.neighborID, dec("40"), "Synthetic ledger", time.Now()); err != nil {
		t.Fatal(err)
	}
	// The fixture reserves one connection for its company advisory lock. One
	// remaining connection proves the three readers release rows before moving on.
	f.pool.SetMaxOpenConns(2)
	for range 2 {
		report := allocationReadinessWithoutMutation(t, f)
		if report.CheckedAccounts != 1 || report.ReadyAccounts != 1 || len(report.Issues) != 0 {
			t.Fatalf("valid account report=%+v", report)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.store.CheckInvoiceAllocationReadiness(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled report err=%v", err)
	}
	if _, err := f.store.CheckInvoiceAllocationReadiness(t.Context()); err != nil {
		t.Fatalf("readiness did not release its canceled transaction: %v", err)
	}
}

func TestInvoiceAllocationReadinessPreservesHistoricalAttributionIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	if _, err := f.store.StornoInvoice(t.Context(), f.yearID, f.neighborID, "Synthetic replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.IssueInvoice(t.Context(), f.yearID, f.neighborID, 2026, time.Now()); err != nil {
		t.Fatal(err)
	}
	var attributed int
	if err := f.pool.QueryRowContext(t.Context(), `SELECT count(*) FROM payments WHERE invoice_id=$1`, f.invoice.ID).Scan(&attributed); err != nil {
		t.Fatal(err)
	}
	if attributed != 1 {
		t.Fatalf("fixture lost canceled historical attribution: count=%d", attributed)
	}
	report := allocationReadinessWithoutMutation(t, f)
	if report.CheckedAccounts != 1 || report.ReadyAccounts != 1 || len(report.Issues) != 0 {
		t.Fatalf("historical invoice_id was interpreted as an active preference: %+v", report)
	}
}

func insertLegacyReadinessRows(t *testing.T, f invoiceSourceFixture, payment, ledger bool) (paymentID, ledgerID int64) {
	t.Helper()
	tx, err := f.pool.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// This fixture owns an empty disposable database. Only while inserting the
	// historical rows, disable the two write guards; re-enable before commit.
	for _, query := range []string{
		`ALTER TABLE payments DISABLE TRIGGER payments_amount_cents_ins`,
		`ALTER TABLE neighbor_ledger DISABLE TRIGGER neighbor_ledger_amount_cents_ins`,
	} {
		if _, err := tx.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if payment {
		if err := tx.QueryRowContext(t.Context(),
			`INSERT INTO payments (billing_year_id,neighbor_id,amount,paid_on) VALUES ($1,$2,9.995,CURRENT_DATE) RETURNING id`,
			f.yearID, f.neighborID).Scan(&paymentID); err != nil {
			t.Fatal(err)
		}
	}
	if ledger {
		if err := tx.QueryRowContext(t.Context(),
			`INSERT INTO neighbor_ledger (billing_year_id,neighbor_id,amount) VALUES ($1,$2,-0.005) RETURNING id`,
			f.yearID, f.neighborID).Scan(&ledgerID); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`ALTER TABLE payments ENABLE TRIGGER payments_amount_cents_ins`,
		`ALTER TABLE neighbor_ledger ENABLE TRIGGER neighbor_ledger_amount_cents_ins`,
	} {
		if _, err := tx.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return paymentID, ledgerID
}

func TestInvoiceAllocationReadinessFlagsLegacyPrecisionWithoutRoundingIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	insertLegacyReadinessRows(t, f, true, true)
	sources, err := f.store.CheckInvoiceSourceAssignments(t.Context())
	if err != nil || sources.Matched != 1 || len(sources.Issues) != 0 {
		t.Fatalf("precision fixture unexpectedly changed source matching: %+v (%v)", sources, err)
	}
	report := allocationReadinessWithoutMutation(t, f)
	if report.CheckedAccounts != 1 || report.ReadyAccounts != 0 {
		t.Fatalf("legacy precision marked ready: %+v", report)
	}
	reasons := map[string]bool{}
	for _, issue := range report.Issues {
		reasons[issue.Reason] = true
	}
	if len(report.Issues) != 2 || !reasons["legacy_fractional_payment"] || !reasons["legacy_fractional_ledger_net"] {
		t.Fatalf("wrong precision diagnostics: %+v", report.Issues)
	}
}

func TestInvoiceAllocationReadinessRestorableLegacyPrecisionIntegration(t *testing.T) {
	for _, tt := range []struct {
		name, reason, amount string
		payment              bool
	}{
		{name: "payment", reason: "legacy_fractional_payment", amount: "9.995", payment: true},
		{name: "ledger", reason: "legacy_fractional_ledger_net", amount: "-0.005"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newInvoiceSourceFixture(t, "regel")
			paymentID, ledgerID := insertLegacyReadinessRows(t, f, tt.payment, !tt.payment)
			assertReadiness := func(wantIssue bool) {
				t.Helper()
				report := allocationReadinessWithoutMutation(t, f)
				if report.CheckedAccounts != 1 {
					t.Fatalf("wrong checked account count: %+v", report)
				}
				if wantIssue {
					if report.ReadyAccounts != 0 || len(report.Issues) != 1 || report.Issues[0].Reason != tt.reason {
						t.Fatalf("active legacy row must be unsupported: %+v", report)
					}
				} else if report.ReadyAccounts != 1 || len(report.Issues) != 0 {
					t.Fatalf("inactive legacy row must be excluded from active-only readiness: %+v", report)
				}
				// Delete/restore and void/unvoid are still legal legacy operations.
				// Neither they nor this read-only report may round the stored amount.
				query, id := `SELECT amount FROM neighbor_ledger WHERE id=$1`, ledgerID
				if tt.payment {
					query, id = `SELECT amount FROM payments WHERE id=$1`, paymentID
				}
				amount := dec("0")
				if err := f.pool.QueryRowContext(t.Context(), query, id).Scan(&amount); err != nil {
					t.Fatal(err)
				}
				if !amount.Equal(dec(tt.amount)) {
					t.Fatalf("historical amount changed: got %s want %s", amount, tt.amount)
				}
			}
			assertReadiness(true)
			if tt.payment {
				if changed, err := f.store.DeletePayment(t.Context(), paymentID); err != nil || !changed {
					t.Fatalf("delete historical payment: changed=%t err=%v", changed, err)
				}
			} else if err := f.store.SetLedgerVoided(t.Context(), ledgerID, true, "Synthetic legacy void"); err != nil {
				t.Fatal(err)
			}
			assertReadiness(false)
			if tt.payment {
				if changed, err := f.store.RestorePayment(t.Context(), paymentID); err != nil || !changed {
					t.Fatalf("restore historical payment: changed=%t err=%v", changed, err)
				}
			} else if err := f.store.SetLedgerVoided(t.Context(), ledgerID, false, ""); err != nil {
				t.Fatal(err)
			}
			assertReadiness(true)
		})
	}
}

func TestInvoiceAllocationReadinessRejectsForeignRelationshipsIntegration(t *testing.T) {
	f := newInvoiceSourceFixture(t, "regel")
	neighborID, err := f.store.CreateNeighbor(t.Context(), "Synthetic other account", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddNeighborToYear(t.Context(), f.yearID, neighborID); err != nil {
		t.Fatal(err)
	}
	var paymentID int64
	if err := f.pool.QueryRowContext(t.Context(), `SELECT id FROM payments WHERE billing_year_id=$1 AND neighbor_id=$2`,
		f.yearID, f.neighborID).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	// Existing foreign keys prove existence, not account ownership. These rows
	// model incompatible historical relationships only in this scratch database.
	if _, err := f.pool.ExecContext(t.Context(), `INSERT INTO payments
		(billing_year_id,neighbor_id,amount,paid_on,reversal_of_payment_id) VALUES ($1,$2,-25,CURRENT_DATE,$3)`,
		f.yearID, neighborID, paymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), `INSERT INTO invoices
		(billing_year_id,neighbor_id,number,kind,status,gross,references_invoice_id)
		VALUES ($1,$2,'SYNTHETIC-ALLOCATION-CREDIT','gutschrift','issued',-10,$3)`,
		f.yearID, neighborID, f.invoice.ID); err != nil {
		t.Fatal(err)
	}
	report := allocationReadinessWithoutMutation(t, f)
	if report.CheckedAccounts != 2 || report.ReadyAccounts != 1 || len(report.Issues) != 2 {
		t.Fatalf("wrong ownership report: %+v", report)
	}
	for _, issue := range report.Issues {
		if issue.NeighborID != neighborID || (issue.Reason != "reversal_target_account_mismatch" && issue.Reason != "credit_target_account_mismatch") {
			t.Fatalf("wrong relationship diagnostic: %+v", issue)
		}
	}
}
