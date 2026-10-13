package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/calc"
)

// DunningRow is one overdue invoice in a billing year: an issued invoice whose
// remaining payable is still positive and whose due date (issue date + payment
// term) has passed.
type DunningRow struct {
	InvoiceID   int64
	YearID      int64
	YearNo      int
	NeighborID  int64
	Name        string
	InvoiceNo   string
	IssuedOn    time.Time
	DueOn       time.Time
	TermDays    int // effective term: the neighbor's override or the company default
	DaysOverdue int
	Open        decimal.Decimal // remaining payable on the issued invoice (> 0)
}

// DunningRows returns one row per overdue active invoice. Account offsets and
// cash are allocated once for the entire snapshot, never once per document.
func (s *Store) DunningRows(ctx context.Context, yearID int64, termDays int, asOf time.Time) ([]DunningRow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	all, err := invoiceOpenRows(ctx, tx, yearID, termDays)
	if err != nil {
		return nil, err
	}
	var out []DunningRow
	for _, row := range all {
		if !row.Open.IsPositive() || !asOf.After(row.DueOn) {
			continue
		}
		if days := calc.DaysBetween(row.DueOn, asOf); days > 0 {
			row.DaysOverdue = days
		}
		out = append(out, row)
	}
	return out, tx.Commit()
}

// invoiceOpenRows shares the same snapshot and allocation inputs with the
// closing checklist. Materialize metadata before issuing the allocation queries.
func invoiceOpenRows(ctx context.Context, tx *sql.Tx, yearID int64, termDays int) ([]DunningRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT iv.id,iv.billing_year_id,y.year,iv.neighbor_id,n.name,
		iv.number,iv.issued_on,COALESCE(n.payment_term_days,$2)
		FROM invoices iv JOIN neighbors n ON n.id=iv.neighbor_id JOIN billing_years y ON y.id=iv.billing_year_id
		WHERE ($1::bigint=0 OR iv.billing_year_id=$1) AND iv.kind='invoice' AND iv.status='issued'
		ORDER BY iv.issued_on,n.name,iv.id`, yearID, termDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DunningRow
	for rows.Next() {
		var row DunningRow
		if err := rows.Scan(&row.InvoiceID, &row.YearID, &row.YearNo, &row.NeighborID, &row.Name,
			&row.InvoiceNo, &row.IssuedOn, &row.TermDays); err != nil {
			return nil, err
		}
		row.DueOn = row.IssuedOn.AddDate(0, 0, row.TermDays)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	accounts, err := invoiceAllocationsTx(ctx, tx, yearID, 0)
	if err != nil {
		return nil, err
	}
	remaining := make(map[int64]decimal.Decimal, len(out))
	for _, account := range accounts {
		for _, invoice := range account.Invoices {
			remaining[invoice.InvoiceID] = invoice.Remaining
		}
	}
	for i := range out {
		out[i].Open = remaining[out[i].InvoiceID]
	}
	return out, nil
}
