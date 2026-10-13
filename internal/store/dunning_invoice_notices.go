package store

import (
	"context"
	"time"
)

// LastDunningNoticesByInvoice resolves historical notice numbers within their
// exact account/year. Zero yearID includes all years; map keys are invoice IDs.
func (s *Store) LastDunningNoticesByInvoice(ctx context.Context, yearID int64) (map[int64]*DunningNotice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON (iv.id) iv.id,d.id,d.billing_year_id,d.neighbor_id,
		d.invoice_number,d.stage,d.channel,d.sent_at,COALESCE(d.grace_until,'0001-01-01'::date),d.fee
		FROM dunning_notices d JOIN invoices iv ON iv.billing_year_id=d.billing_year_id
		  AND iv.neighbor_id=d.neighbor_id AND iv.number=d.invoice_number AND iv.kind='invoice'
		WHERE ($1::bigint=0 OR d.billing_year_id=$1)
		ORDER BY iv.id,d.sent_at DESC,d.id DESC`, yearID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]*DunningNotice)
	for rows.Next() {
		var invoiceID int64
		notice := new(DunningNotice)
		if err := rows.Scan(&invoiceID, &notice.ID, &notice.BillingYearID, &notice.NeighborID, &notice.InvoiceNumber,
			&notice.Stage, &notice.Channel, &notice.SentAt, &notice.GraceUntil, &notice.Fee); err != nil {
			return nil, err
		}
		if notice.GraceUntil.Year() <= 1 {
			notice.GraceUntil = time.Time{}
		}
		out[invoiceID] = notice
	}
	return out, rows.Err()
}
