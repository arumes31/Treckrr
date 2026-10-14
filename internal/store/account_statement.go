package store

import (
	"context"
	"database/sql"
	"fmt"
)

type statementQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// NeighborStatement keeps year totals and actual payment history in one
// snapshot so a concurrent payment cannot make its HTML/PDF contradict itself.
type NeighborStatement struct {
	Years    []NeighborYearHistoryRow
	Payments []PaymentRow
}

// ReadNeighborStatement uses one read-only snapshot for account HTML and PDF;
// it never changes payment history, signed yearly balances or invoice values.
func (s *Store) ReadNeighborStatement(ctx context.Context, neighborID int64) (NeighborStatement, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return NeighborStatement{}, fmt.Errorf("account statement: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	years, err := neighborYearHistory(ctx, tx, neighborID)
	if err != nil {
		return NeighborStatement{}, fmt.Errorf("account statement: years: %w", err)
	}
	payments, err := neighborPayments(ctx, tx, neighborID)
	if err != nil {
		return NeighborStatement{}, fmt.Errorf("account statement: payments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return NeighborStatement{}, fmt.Errorf("account statement: finish: %w", err)
	}
	return NeighborStatement{Years: years, Payments: payments}, nil
}
