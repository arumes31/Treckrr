package store

import (
	"context"
	"errors"
)

var errStaleDunningMail = errors.New("stale reminder payment demand")

const staleDunningMailReason = "Zahlungsstand der Mahnung veraltet; bitte eine neue Mahnung erstellen."

// dunningMailCurrent checks the live document allocation before a reminder is
// claimed and again before SMTP DATA. Legacy messages without an exact amount
// are held rather than guessing what their retained attachment requests.
func (s *Store) dunningMailCurrent(ctx context.Context, m OutboxMail) (bool, error) {
	if m.Kind != "mahnung" {
		return true, nil
	}
	if m.InvoiceID <= 0 || m.NeighborID <= 0 || m.BillingYearID <= 0 ||
		m.Meta.OpenAmount == nil || !m.Meta.OpenAmount.IsPositive() || !m.Meta.Fee.IsZero() {
		return false, nil
	}
	account, err := s.AccountInvoiceAllocations(ctx, m.BillingYearID, m.NeighborID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, invoice := range account.Invoices {
		if invoice.InvoiceID == m.InvoiceID {
			return invoice.Remaining.IsPositive() && invoice.Remaining.Equal(*m.Meta.OpenAmount), nil
		}
	}
	return false, nil
}

// holdStaleDunningMail keeps the original payload and identity for inspection.
// Only the current owner may hold a sending attempt; a concurrent sender is
// never changed by a pre-claim check.
func (s *Store) holdStaleDunningMail(ctx context.Context, m OutboxMail, expected string, attemptSeq int) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE mail_outbox
		SET status='held', held_at=now(), claimed_at=NULL, terminal_at=NULL,
		    delivery_phase=NULL, last_error=$4
		WHERE id=$1 AND status=$2 AND ($3=0 OR attempt_seq=$3)
		  AND (message_id IS NULL OR message_id='' OR message_id=$5)`,
		m.ID, expected, attemptSeq, staleDunningMailReason, m.MessageID)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed == 0 {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM mail_outbox WHERE id=$1`, m.ID).Scan(&status)
		return status, err
	}
	if attemptSeq > 0 {
		if err := finishMailAttemptTx(ctx, tx, m.ID, attemptSeq, "failed", staleDunningMailReason); err != nil {
			return "", err
		}
	}
	if err := addMailAuditTx(withSystemAuditActor(ctx), tx, "mail_held_stale_reminder", m, staleDunningMailReason); err != nil {
		return "", err
	}
	return MailStatusHeld, tx.Commit()
}
