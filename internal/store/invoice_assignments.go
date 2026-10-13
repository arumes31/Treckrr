package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/d0linger/treckrr/internal/models"
)

// ErrInvoiceAmbiguous requires explicit selection instead of an arbitrary invoice.
var ErrInvoiceAmbiguous = errors.New("multiple active invoices require selection")

// ErrNoPendingEntries means that no non-voided, unassigned work can be invoiced.
var ErrNoPendingEntries = errors.New("no pending entries to invoice")

// ListActiveInvoices returns every active invoice in stable FIFO order.
func (s *Store) ListActiveInvoices(ctx context.Context, yearID, neighborID int64) ([]models.Invoice, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+invoiceCols+` FROM invoices
		 WHERE billing_year_id=$1 AND neighbor_id=$2 AND kind='invoice' AND status='issued'
		 ORDER BY issued_on,id`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectInvoiceRows(rows)
}

func collectInvoiceRows(rows *sql.Rows) ([]models.Invoice, error) {
	invoices := make([]models.Invoice, 0)
	for rows.Next() {
		invoice, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		invoices = append(invoices, invoice)
	}
	return invoices, rows.Err()
}

func uniqueInvoice(invoices []models.Invoice) (models.Invoice, error) {
	switch len(invoices) {
	case 0:
		return models.Invoice{}, ErrNotFound
	case 1:
		return invoices[0], nil
	default:
		return models.Invoice{}, ErrInvoiceAmbiguous
	}
}

// activeInvoiceTx validates document identity after the caller locks its account.
// Zero is the backwards-compatible, unique-only selection.
func activeInvoiceTx(
	ctx context.Context, tx *sql.Tx, yearID, neighborID, invoiceID int64,
) (models.Invoice, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+invoiceCols+` FROM invoices
		 WHERE billing_year_id=$1 AND neighbor_id=$2 AND kind='invoice' AND status='issued'
		   AND ($3::bigint=0 OR id=$3) ORDER BY issued_on,id FOR UPDATE`,
		yearID, neighborID, invoiceID)
	if err != nil {
		return models.Invoice{}, err
	}
	defer rows.Close()
	invoices, err := collectInvoiceRows(rows)
	if err != nil {
		return models.Invoice{}, err
	}
	return uniqueInvoice(invoices)
}

// BuildPendingInvoiceContent previews only work not claimed by an active invoice.
// Its hash also binds ordered source IDs, so identical later work is not a replay.
// The historical BuildInvoiceContent projection remains unchanged.
func (s *Store) BuildPendingInvoiceContent(
	ctx context.Context, yearID, neighborID int64,
) (models.InvoiceContent, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return models.InvoiceContent{}, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit; preview writes nothing
	content, err := s.buildInvoiceContentForSourcesTx(ctx, tx, yearID, neighborID, true)
	if err != nil {
		return models.InvoiceContent{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.InvoiceContent{}, err
	}
	return content, nil
}

// buildInvoiceContentTx retains the all-source projection for legacy snapshots
// and pre-invoice credit limits, never for subsequent invoice issuance.
func (s *Store) buildInvoiceContentTx(
	ctx context.Context, tx *sql.Tx, yearID, neighborID int64,
) (models.InvoiceContent, error) {
	return s.buildInvoiceContentForSourcesTx(ctx, tx, yearID, neighborID, false)
}

func invoiceSourceHash(contentHash string, entries []models.Entry) string {
	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	// These primitive fields have no failing JSON encoders.
	body, _ := json.Marshal(struct {
		Content string
		Sources []int64
	}{Content: contentHash, Sources: ids})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// claimInvoiceSources uses the same locked pending set as the frozen projection.
// A partial claim, constraint failure or hash write rolls back the whole invoice.
func claimInvoiceSources(ctx context.Context, tx *sql.Tx, invoice models.Invoice, requestHash string) error {
	if invoice.Content == nil || len(invoice.Content.Lines) == 0 || requestHash == "" {
		return ErrInvoiceIncomplete
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO invoice_entries(invoice_id,entry_id,source_entry_id,ordinal,active)
		SELECT $1,e.id,e.id,row_number() OVER(ORDER BY e.entry_date,e.id)::integer,true
		  FROM entries e
		 WHERE e.billing_year_id=$2 AND e.neighbor_id=$3 AND NOT e.voided
		   AND NOT EXISTS(SELECT 1 FROM invoice_entries ie WHERE ie.entry_id=e.id AND ie.active)`,
		invoice.ID, invoice.BillingYearID, invoice.NeighborID)
	if err != nil {
		return fmt.Errorf("claim invoice sources: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != int64(len(invoice.Content.Lines)) {
		return ErrInvoiceContentChanged
	}
	result, err = tx.ExecContext(ctx, `UPDATE invoices SET issue_request_hash=$2 WHERE id=$1`, invoice.ID, requestHash)
	if err != nil {
		return err
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}
