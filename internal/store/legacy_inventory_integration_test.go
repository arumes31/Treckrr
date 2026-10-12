//go:build integration

package store_test

import (
	"testing"
)

func TestLegacyInventoryIsReadOnly(t *testing.T) {
	st, pool, yearID, neighborID := scratchBookingFixture(t)
	for _, booking := range []any{
		nil,
		`{"version":1,"kind":"machine","neighbor_equipment_id":5}`,
		`{"version":1,"kind":"labor","quantity":"2","unit_price":"12"}`,
		`{"version":2,"kind":"labor","people":[{"hours":"2","rate":"12"}]}`,
	} {
		if _, err := pool.ExecContext(t.Context(), `INSERT INTO neighbor_ledger (billing_year_id, neighbor_id, amount, description, posting_date, booking) VALUES ($1,$2,-24,'synthetic historical fixture','2026-10-12',$3::jsonb)`, yearID, neighborID, booking); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.ExecContext(t.Context(), `INSERT INTO recurring_entries (neighbor_id,template,interval_kind,next_run) VALUES ($1,'{"companion":{"person_id":42,"name":"synthetic"}}','monthly','2026-11-12')`, neighborID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRowContext(t.Context(), `SELECT jsonb_agg(to_jsonb(n) ORDER BY id)::text FROM neighbor_ledger n`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	report, err := st.ReadLegacyInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.ManualLedger != 1 || report.LegacyEquipment != 1 || report.LegacyPeople != 1 || report.LegacyRecurring != 1 {
		t.Fatalf("unexpected inventory: %+v", report)
	}
	var after string
	if err := pool.QueryRowContext(t.Context(), `SELECT jsonb_agg(to_jsonb(n) ORDER BY id)::text FROM neighbor_ledger n`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("inventory modified historical rows")
	}
}
