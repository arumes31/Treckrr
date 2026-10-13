package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/d0linger/treckrr/internal/models"
)

type invoiceSchemaReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// invoiceSourcesAvailable also works against old restored schemas. It never
// changes schema or treats absent provenance as permission to assign sources.
func invoiceSourcesAvailable(ctx context.Context, query invoiceSchemaReader) (bool, error) {
	var available bool
	if err := query.QueryRowContext(ctx, `SELECT to_regclass('invoice_entries') IS NOT NULL`).Scan(&available); err != nil {
		return false, fmt.Errorf("inspect invoice source schema: %w", err)
	}
	return available, nil
}

// listInvoiceSourcesWithoutClaims is the historical annual content projection,
// including before 0081. Derived claim flags do not participate in invoice math.
func (s *Store) listInvoiceSourcesWithoutClaims(ctx context.Context, neighborID, yearID int64) ([]models.Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryCols+`, false AS invoice_locked FROM entries
		WHERE neighbor_id=$1 AND billing_year_id=$2 ORDER BY entry_date,id`, neighborID, yearID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEntries(rows)
}

type assignedSourceScanner struct {
	rows      *sql.Rows
	invoiceID *int64
	ordinal   *int64
}

func (s assignedSourceScanner) Scan(dest ...any) error {
	return s.rows.Scan(append([]any{s.invoiceID, s.ordinal}, dest...)...)
}

func readAssignedInvoiceSources(
	ctx context.Context, tx *sql.Tx,
) (map[int64][]models.Entry, map[int64]string, error) {
	// Only entries has the names in entryCols, so unqualified legacy columns
	// keep their scan order without coupling the preflight to new schema fields.
	rows, err := tx.QueryContext(ctx, `SELECT ie.invoice_id,ie.ordinal,`+entryCols+`,true AS invoice_locked
		FROM entries JOIN invoice_entries ie ON ie.entry_id=entries.id
		WHERE ie.active ORDER BY ie.invoice_id,ie.ordinal`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byInvoice := make(map[int64][]models.Entry)
	issues := make(map[int64]string)
	for rows.Next() {
		var invoiceID, ordinal int64
		entry, err := scanEntry(assignedSourceScanner{rows: rows, invoiceID: &invoiceID, ordinal: &ordinal})
		if err != nil {
			return nil, nil, err
		}
		if ordinal != int64(len(byInvoice[invoiceID])+1) {
			issues[invoiceID] = "source_assignment_mismatch"
		}
		byInvoice[invoiceID] = append(byInvoice[invoiceID], entry)
	}
	return byInvoice, issues, rows.Err()
}
