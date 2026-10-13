package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

// RetainedPayment includes a soft-deleted payment still held in the database.
// It is for subject access, not balances or ordinary payment lists.
type RetainedPayment struct {
	models.Payment
	DeletedAt          *time.Time
	PreferredInvoiceID *int64
}

// ListRetainedPayments includes deleted_at so an access export cannot silently
// omit records merely hidden from the ordinary payment history.
func (s *Store) ListRetainedPayments(ctx context.Context, yearID, neighborID int64) ([]RetainedPayment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.billing_year_id, p.neighbor_id, p.amount, p.paid_on, p.note,
		        p.method, p.invoice_id, COALESCE(iv.number,''),
		        (SELECT r.batch_id FROM payment_import_rows r
		          WHERE r.payment_id=p.id OR r.reversal_payment_id=p.id LIMIT 1),
		        p.reversal_of_payment_id IS NOT NULL, p.created_at, p.deleted_at, p.preferred_invoice_id
		   FROM payments p LEFT JOIN invoices iv ON iv.id=p.invoice_id
		  WHERE p.billing_year_id=$1 AND p.neighbor_id=$2 ORDER BY p.paid_on, p.id`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RetainedPayment{}
	for rows.Next() {
		var p RetainedPayment
		if err := rows.Scan(&p.ID, &p.BillingYearID, &p.NeighborID, &p.Amount, &p.PaidOn, &p.Note,
			&p.Method, &p.InvoiceID, &p.InvoiceNumber, &p.ImportBatchID, &p.Reversal, &p.Created,
			&p.DeletedAt, &p.PreferredInvoiceID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// NeighborInvoiceSourceExport retains both active claims and released historical
// provenance. EntryID is nil if an unbilled source was deleted after cancellation.
type NeighborInvoiceSourceExport struct {
	InvoiceID     int64  `json:"invoice_id"`
	EntryID       *int64 `json:"entry_id,omitempty"`
	SourceEntryID int64  `json:"source_entry_id"`
	Ordinal       int    `json:"ordinal"`
	Active        bool   `json:"active"`
}

// ListNeighborInvoiceSourceExport scopes provenance through its retained invoice,
// including inactive claims whose original entry is no longer present.
func (s *Store) ListNeighborInvoiceSourceExport(ctx context.Context, yearID, neighborID int64) ([]NeighborInvoiceSourceExport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ie.invoice_id,ie.entry_id,ie.source_entry_id,ie.ordinal,ie.active
		FROM invoice_entries ie JOIN invoices iv ON iv.id=ie.invoice_id
		WHERE iv.billing_year_id=$1 AND iv.neighbor_id=$2
		ORDER BY ie.invoice_id,ie.ordinal`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NeighborInvoiceSourceExport
	for rows.Next() {
		var source NeighborInvoiceSourceExport
		if err := rows.Scan(&source.InvoiceID, &source.EntryID, &source.SourceEntryID, &source.Ordinal, &source.Active); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

// NeighborPaymentImportExport is the personal transaction metadata retained in
// the payment-import journal for one neighbor.
type NeighborPaymentImportExport struct {
	BatchID         int64           `json:"batch_id"`
	RowNo           int             `json:"row_no"`
	TransactionDate *time.Time      `json:"transaction_date,omitempty"`
	Amount          decimal.Decimal `json:"amount"`
	Reference       string          `json:"reference,omitempty"`
	PayerName       string          `json:"payer_name,omitempty"`
	PayerIBAN       string          `json:"payer_iban,omitempty"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
}

// ListNeighborPaymentImportExport returns journal rows directly attributed to
// the subject in a billing year, including unmatched business refusals.
func (s *Store) ListNeighborPaymentImportExport(ctx context.Context, yearID, neighborID int64) ([]NeighborPaymentImportExport, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT batch_id, row_no, transaction_date, amount, reference,
		       payer_name, payer_iban, status, reason
		  FROM payment_import_rows
		 WHERE billing_year_id=$1 AND neighbor_id=$2 ORDER BY batch_id, row_no`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NeighborPaymentImportExport
	for rows.Next() {
		var row NeighborPaymentImportExport
		if err := rows.Scan(&row.BatchID, &row.RowNo, &row.TransactionDate, &row.Amount,
			&row.Reference, &row.PayerName, &row.PayerIBAN, &row.Status, &row.Reason); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// NeighborRecurringExport is independent of billing-year membership: a stored
// template may be the only remaining record for this neighbor.
type NeighborRecurringExport struct {
	ID           int64                `json:"id"`
	Template     models.RecurTemplate `json:"template"`
	IntervalKind string               `json:"interval_kind"`
	NextRun      time.Time            `json:"next_run"`
	EndsOn       *time.Time           `json:"ends_on,omitempty"`
	Active       bool                 `json:"active"`
	CreatedAt    time.Time            `json:"created_at"`
	LastRunAt    *time.Time           `json:"last_run_at,omitempty"`
}

// ListNeighborRecurringExport scopes templates directly to the subject, including
// templates whose next occurrence has no billing year yet.
func (s *Store) ListNeighborRecurringExport(ctx context.Context, neighborID int64) ([]NeighborRecurringExport, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, template, interval_kind, next_run, ends_on, active, created_at, last_run_at
		   FROM recurring_entries WHERE neighbor_id=$1 ORDER BY id`, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NeighborRecurringExport{}
	for rows.Next() {
		var rule NeighborRecurringExport
		var blob []byte
		if err := rows.Scan(&rule.ID, &blob, &rule.IntervalKind, &rule.NextRun, &rule.EndsOn,
			&rule.Active, &rule.CreatedAt, &rule.LastRunAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(blob, &rule.Template); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// NeighborMailExport includes subject correspondence and attachment metadata,
// never attachment bytes or SMTP errors (which can contain server secrets).
type NeighborMailExport struct {
	ID              int64      `json:"id"`
	BillingYearID   *int64     `json:"billing_year_id,omitempty"`
	InvoiceID       *int64     `json:"invoice_id,omitempty"`
	Kind            string     `json:"kind"`
	Recipient       string     `json:"recipient"`
	Subject         string     `json:"subject"`
	Body            string     `json:"body"`
	AttachmentName  string     `json:"attachment_name,omitempty"`
	AttachmentType  string     `json:"attachment_type,omitempty"`
	AttachmentBytes int64      `json:"attachment_bytes"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	CreatedAt       time.Time  `json:"created_at"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
}

// ListNeighborMailExport includes pending, failed and sent correspondence for the
// subject. Attachments are described without loading their binary contents.
func (s *Store) ListNeighborMailExport(ctx context.Context, neighborID int64) ([]NeighborMailExport, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, billing_year_id, invoice_id, kind, recipient, subject, body, att_name, att_type,
		        COALESCE(octet_length(att_data),0), status, attempts, created_at, sent_at
		   FROM mail_outbox WHERE neighbor_id=$1 ORDER BY id`, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NeighborMailExport{}
	for rows.Next() {
		var mail NeighborMailExport
		if err := rows.Scan(&mail.ID, &mail.BillingYearID, &mail.InvoiceID, &mail.Kind, &mail.Recipient, &mail.Subject,
			&mail.Body, &mail.AttachmentName, &mail.AttachmentType, &mail.AttachmentBytes,
			&mail.Status, &mail.Attempts, &mail.CreatedAt, &mail.SentAt); err != nil {
			return nil, err
		}
		out = append(out, mail)
	}
	return out, rows.Err()
}
