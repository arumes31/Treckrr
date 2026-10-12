package store

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
)

// ErrCarryPreviewChanged prevents confirming an out-of-date transfer amount.
var ErrCarryPreviewChanged = errors.New("carry-forward preview changed")

// CarryPreview contains the two payable balances shown before a transfer.
type CarryPreview struct {
	Source decimal.Decimal
	Target decimal.Decimal
}

// TargetAfter applies the source balance to the destination without treating
// the transfer as a payment. Negative source balances transfer credit.
func (p CarryPreview) TargetAfter() decimal.Decimal { return p.Target.Add(p.Source) }

// PreviewCarryForward reads both accounts under the same lock ordering used by
// the write path. It creates no postings and never changes either account.
func (s *Store) PreviewCarryForward(ctx context.Context, neighborID, fromYearID, toYearID int64) (CarryPreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CarryPreview{}, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only transaction
	if err := lockSettlementAccounts(ctx, tx,
		accountKey{yearID: fromYearID, neighborID: neighborID},
		accountKey{yearID: toYearID, neighborID: neighborID}); err != nil {
		return CarryPreview{}, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM billing_years WHERE id=$1`, toYearID).Scan(&status); err != nil {
		return CarryPreview{}, err
	}
	if status == "completed" {
		return CarryPreview{}, ErrCarryTargetCompleted
	}
	source, err := remainingLocked(ctx, tx, fromYearID, neighborID)
	if err != nil {
		return CarryPreview{}, err
	}
	target, err := remainingLocked(ctx, tx, toYearID, neighborID)
	if err != nil {
		return CarryPreview{}, err
	}
	return CarryPreview{Source: source, Target: target}, nil
}

// TransferTarget links a ledger posting to its counterpart in another year.
type TransferTarget struct {
	LedgerID int64
	YearID   int64
	Year     int
}

// LedgerTransferTargets returns counterpart links for this account in one query.
// Voided pairs remain linked because they are part of the account history.
func (s *Store) LedgerTransferTargets(ctx context.Context, yearID, neighborID int64) (map[int64]TransferTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source.id, target.id, target.billing_year_id, y.year
		FROM neighbor_ledger source
		JOIN neighbor_ledger target ON target.transfer_id=source.transfer_id AND target.id<>source.id AND target.neighbor_id=source.neighbor_id
		JOIN billing_years y ON y.id=target.billing_year_id
		WHERE source.billing_year_id=$1 AND source.neighbor_id=$2 AND source.transfer_id<>''`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]TransferTarget)
	for rows.Next() {
		var id int64
		var target TransferTarget
		if err := rows.Scan(&id, &target.LedgerID, &target.YearID, &target.Year); err != nil {
			return nil, err
		}
		out[id] = target
	}
	return out, rows.Err()
}
