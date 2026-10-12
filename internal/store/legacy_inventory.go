package store

import (
	"context"
	"fmt"
)

// LegacyInventory is an aggregate-only, read-only compatibility report. Counts
// describe stored shapes, not corrupt data, and deliberately exclude identities.
type LegacyInventory struct {
	Entries                 int64
	LegacyReplayEntries     int64
	LabelOnlyMachines       int64
	ManualLedger            int64
	LegacyEquipment         int64
	LegacyPeople            int64
	LegacyRecurring         int64
	InvoiceSnapshotsMissing int64
}

// ReadLegacyInventory identifies adapters still needed by historical rows.
// No migration/backfill is implied by a positive count; old prices stay frozen.
func (s *Store) ReadLegacyInventory(ctx context.Context) (LegacyInventory, error) {
	var out LegacyInventory
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM entries),
		(SELECT count(*) FROM entries WHERE idempotency_key IS NOT NULL AND request_fingerprint IS NULL),
		(SELECT count(*) FROM entries e WHERE machine_labels <> '' AND NOT EXISTS (SELECT 1 FROM entry_machines em WHERE em.entry_id=e.id)),
		(SELECT count(*) FROM neighbor_ledger WHERE booking IS NULL),
		(SELECT count(*) FROM neighbor_ledger WHERE booking->>'neighbor_equipment_id' IS NOT NULL),
		(SELECT count(*) FROM neighbor_ledger WHERE booking IS NOT NULL AND (booking->'people' IS NULL OR booking->'people'='null'::jsonb OR booking->'people'='[]'::jsonb) AND (booking->>'kind'='labor' OR COALESCE(booking->>'person_hours','0') NOT IN ('0','0.0',''))),
		(SELECT count(*) FROM recurring_entries WHERE template->'companion' IS NOT NULL AND template->'companion'<>'null'::jsonb AND (template->'companions' IS NULL OR template->'companions'='null'::jsonb OR template->'companions'='[]'::jsonb)),
		(SELECT count(*) FROM invoices WHERE kind='invoice' AND (issuer IS NULL OR recipient IS NULL OR lines IS NULL OR content_hash=''))`).Scan(
		&out.Entries, &out.LegacyReplayEntries, &out.LabelOnlyMachines, &out.ManualLedger,
		&out.LegacyEquipment, &out.LegacyPeople, &out.LegacyRecurring, &out.InvoiceSnapshotsMissing)
	if err != nil {
		return out, fmt.Errorf("read historical compatibility inventory: %w", err)
	}
	return out, nil
}
