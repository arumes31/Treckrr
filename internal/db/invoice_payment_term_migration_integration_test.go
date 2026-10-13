//go:build integration

package db

import "testing"

func TestInvoicePaymentTermMigrationPreservesHistory(t *testing.T) {
	pool := invoiceSourceRehearsalDatabase(t)
	migrateBeforeInvoiceSources(t, pool)
	seedInvoiceSourceRehearsal(t, pool)
	if err := applyMigrationOnce(t.Context(), pool, invoiceSourceRehearsalName, invoiceSourceRehearsalSQL); err != nil {
		t.Fatal(err)
	}
	before := invoiceSourceRehearsalSnapshot(t, pool)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if after := invoiceSourceRehearsalSnapshot(t, pool); after != before {
		t.Fatal("payment-term migration changed historical financial rows or snapshots")
	}
	var unknown bool
	if err := pool.QueryRowContext(t.Context(), `SELECT payment_term_days IS NULL FROM invoices WHERE id=1`).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("legacy term must stay unknown: %v, %v", unknown, err)
	}
	for _, days := range []int{0, 14, 365} {
		if _, err := pool.ExecContext(t.Context(), `UPDATE invoices SET payment_term_days=$1 WHERE id=1`, days); err != nil {
			t.Fatal(err)
		}
	}
	for _, days := range []int{-1, 366} {
		if _, err := pool.ExecContext(t.Context(), `UPDATE invoices SET payment_term_days=$1 WHERE id=1`, days); err == nil {
			t.Fatalf("invalid term %d accepted", days)
		}
	}
}
