//go:build integration

package store_test

import (
	"reflect"
	"testing"
)

// TestReadNeighborStatementPreservesValuesIntegration checks that the snapshot
// statement matches the separate history and payment reads and writes nothing.
func TestReadNeighborStatementPreservesValuesIntegration(t *testing.T) {
	f := newMultiAllocationFixture(t)
	if err := f.store.AddPayment(t.Context(), f.yearID, f.neighborID, dec("70"), day(2026, 9, 12), "private note", "bar"); err != nil {
		t.Fatal(err)
	}
	before := invoiceSourceState(t, f.pool)
	years, err := f.store.NeighborYearHistory(t.Context(), f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	payments, err := f.store.ListNeighborPayments(t.Context(), f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := f.store.ReadNeighborStatement(t.Context(), f.neighborID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(years, statement.Years) || !reflect.DeepEqual(payments, statement.Payments) {
		t.Fatal("statement changed historical/payment values")
	}
	if after := invoiceSourceState(t, f.pool); !reflect.DeepEqual(before, after) {
		t.Fatal("statement read changed financial/audit rows")
	}
}
