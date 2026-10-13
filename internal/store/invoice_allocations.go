package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"
)

// InvoiceAllocation separates frozen gross, document credits, account offsets
// and actual cash. Remaining is nonnegative; excess stays on the account.
type InvoiceAllocation struct {
	InvoiceID      int64
	Gross          decimal.Decimal
	AttachedCredit decimal.Decimal
	OffsetApplied  decimal.Decimal
	Paid           decimal.Decimal
	Remaining      decimal.Decimal
}

// InvoiceAllocationSummary excludes uninvoiced booking net. AccountResidual can
// coexist with open documents; use AccountRemaining for the annual balance and
// account credit, never UnallocatedCash alone.
type InvoiceAllocationSummary struct {
	Invoices        []InvoiceAllocation
	UnallocatedCash decimal.Decimal
	UnusedOffset    decimal.Decimal
	AccountResidual decimal.Decimal
}

// AccountInvoiceAllocations reads one account in a consistent snapshot without
// changing any stored payment, invoice or historical allocation choice.
func (s *Store) AccountInvoiceAllocations(ctx context.Context, yearID, neighborID int64) (InvoiceAllocationSummary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return InvoiceAllocationSummary{}, fmt.Errorf("invoice allocations: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM billing_year_neighbors
		WHERE billing_year_id=$1 AND neighbor_id=$2)`, yearID, neighborID).Scan(&exists); err != nil {
		return InvoiceAllocationSummary{}, fmt.Errorf("invoice allocations: account: %w", err)
	}
	if !exists {
		return InvoiceAllocationSummary{}, ErrNotFound
	}
	accounts, err := invoiceAllocationsTx(ctx, tx, yearID, neighborID)
	if err != nil {
		return InvoiceAllocationSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return InvoiceAllocationSummary{}, fmt.Errorf("invoice allocations: finish: %w", err)
	}
	return accounts[accountKey{yearID: yearID, neighborID: neighborID}], nil
}

// InvoiceDocumentRemaining returns one active invoice's open amount, never the
// annual balance or another invoice. Missing/canceled documents are not found.
func (s *Store) InvoiceDocumentRemaining(ctx context.Context, invoiceID int64) (decimal.Decimal, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return decimal.Zero, fmt.Errorf("invoice remaining: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	var key accountKey
	err = tx.QueryRowContext(ctx, `SELECT billing_year_id,neighbor_id FROM invoices
		WHERE id=$1 AND kind='invoice' AND status='issued'`, invoiceID).Scan(&key.yearID, &key.neighborID)
	if errors.Is(err, sql.ErrNoRows) {
		return decimal.Zero, ErrNotFound
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("invoice remaining: document: %w", err)
	}
	accounts, err := invoiceAllocationsTx(ctx, tx, key.yearID, key.neighborID)
	if err != nil {
		return decimal.Zero, err
	}
	for _, invoice := range accounts[key].Invoices {
		if invoice.InvoiceID == invoiceID {
			if err := tx.Commit(); err != nil {
				return decimal.Zero, fmt.Errorf("invoice remaining: finish: %w", err)
			}
			return invoice.Remaining, nil
		}
	}
	return decimal.Zero, ErrNotFound
}

// invoiceAllocationsTx batches all inputs, with zero scope IDs meaning all.
// Callers hold a repeatable-read snapshot or the canonical account locks.
func invoiceAllocationsTx(ctx context.Context, tx *sql.Tx, yearID, neighborID int64) (map[accountKey]InvoiceAllocationSummary, error) {
	inputs, err := allocationInvoiceInputs(ctx, tx, yearID, neighborID)
	if err != nil {
		return nil, fmt.Errorf("invoice allocations: documents: %w", err)
	}
	if err := allocationPaymentInputs(ctx, tx, yearID, neighborID, inputs); err != nil {
		return nil, fmt.Errorf("invoice allocations: payments: %w", err)
	}
	if err := allocationLedgerInputs(ctx, tx, yearID, neighborID, inputs); err != nil {
		return nil, fmt.Errorf("invoice allocations: ledger: %w", err)
	}
	out := make(map[accountKey]InvoiceAllocationSummary, len(inputs))
	for key, input := range inputs {
		plan, err := planInvoiceAllocation(*input)
		if err != nil {
			return nil, fmt.Errorf("invoice allocations: account %d/%d: %w", key.yearID, key.neighborID, err)
		}
		paid := make(map[int64]decimal.Decimal)
		for _, payment := range plan.Payments {
			paid[payment.InvoiceID] = paid[payment.InvoiceID].Add(payment.Amount)
		}
		slices.SortFunc(input.Invoices, func(a, b allocationInvoice) int {
			return compareAllocationOrder(a.IssuedOn, a.ID, b.IssuedOn, b.ID)
		})
		summary := InvoiceAllocationSummary{
			Invoices:        make([]InvoiceAllocation, 0, len(input.Invoices)),
			UnallocatedCash: plan.UnallocatedCash, UnusedOffset: plan.UnusedOffset,
			AccountResidual: plan.AccountResidual,
		}
		for _, invoice := range input.Invoices {
			summary.Invoices = append(summary.Invoices, InvoiceAllocation{
				InvoiceID: invoice.ID, Gross: invoice.Gross, AttachedCredit: invoice.AttachedCredit,
				OffsetApplied: plan.OffsetApplied[invoice.ID], Paid: paid[invoice.ID], Remaining: plan.Remaining[invoice.ID],
			})
		}
		out[key] = summary
	}
	return out, nil
}

func allocationInputFor(inputs map[accountKey]*invoiceAllocationInput, key accountKey) *invoiceAllocationInput {
	if inputs[key] == nil {
		inputs[key] = &invoiceAllocationInput{}
	}
	return inputs[key]
}

type allocationDocument struct {
	allocationInvoice
	account     accountKey
	kind        string
	referenceID int64
}

func allocationInvoiceInputs(ctx context.Context, tx *sql.Tx, yearID, neighborID int64) (map[accountKey]*invoiceAllocationInput, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,billing_year_id,neighbor_id,kind,issued_on,gross,COALESCE(references_invoice_id,0)
		FROM invoices WHERE status='issued' AND kind IN ('invoice','gutschrift')
		AND ($1::bigint=0 OR billing_year_id=$1) AND ($2::bigint=0 OR neighbor_id=$2) ORDER BY id`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	inputs := make(map[accountKey]*invoiceAllocationInput)
	targets := make(map[int64]allocationDocument)
	var credits []allocationDocument
	for rows.Next() {
		var row allocationDocument
		var gross decimal.NullDecimal
		if err := rows.Scan(&row.ID, &row.account.yearID, &row.account.neighborID,
			&row.kind, &row.IssuedOn, &gross, &row.referenceID); err != nil {
			return nil, err
		}
		if !gross.Valid {
			return nil, fmt.Errorf("%w: invoice %d has no frozen gross", errInvalidAllocationInput, row.ID)
		}
		row.Gross = gross.Decimal
		allocationInputFor(inputs, row.account)
		if row.kind == "invoice" {
			targets[row.ID] = row
		} else {
			credits = append(credits, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	attached := make(map[int64]decimal.Decimal)
	for _, credit := range credits {
		if credit.IssuedOn.IsZero() || credit.Gross.IsPositive() || !wholeAllocationCents(credit.Gross) {
			return nil, fmt.Errorf("%w: credit %d amount or date", errInvalidAllocationInput, credit.ID)
		}
		if credit.referenceID == 0 {
			input := inputs[credit.account]
			input.StandaloneCredits = input.StandaloneCredits.Add(credit.Gross)
			continue
		}
		target, ok := targets[credit.referenceID]
		if !ok || target.account != credit.account {
			return nil, fmt.Errorf("%w: credit %d target", errInvalidAllocationInput, credit.ID)
		}
		attached[target.ID] = attached[target.ID].Add(credit.Gross)
	}
	for _, invoice := range targets {
		invoice.AttachedCredit = attached[invoice.ID]
		input := inputs[invoice.account]
		input.Invoices = append(input.Invoices, invoice.allocationInvoice)
	}
	return inputs, nil
}

func allocationPaymentInputs(ctx context.Context, tx *sql.Tx, yearID, neighborID int64, inputs map[accountKey]*invoiceAllocationInput) error {
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.billing_year_id,p.neighbor_id,p.amount,p.paid_on,
		COALESCE(p.reversal_of_payment_id,0),COALESCE(p.preferred_invoice_id,0),
		COALESCE(iv.billing_year_id,0),COALESCE(iv.neighbor_id,0),COALESCE(iv.kind,''),COALESCE(iv.status,'')
		FROM payments p LEFT JOIN invoices iv ON iv.id=p.preferred_invoice_id
		WHERE p.deleted_at IS NULL AND ($1::bigint=0 OR p.billing_year_id=$1) AND ($2::bigint=0 OR p.neighbor_id=$2)
		ORDER BY p.id`, yearID, neighborID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type paymentRow struct {
		allocationPayment
		account accountKey
	}
	owners := make(map[int64]accountKey)
	var payments []paymentRow
	for rows.Next() {
		var row paymentRow
		var target accountKey
		var kind, status string
		if err := rows.Scan(&row.ID, &row.account.yearID, &row.account.neighborID, &row.Amount,
			&row.PaidOn, &row.ReversalOfID, &row.PreferredInvoiceID, &target.yearID, &target.neighborID, &kind, &status); err != nil {
			return err
		}
		if row.PreferredInvoiceID != 0 {
			if target != row.account || kind != "invoice" || (status != "issued" && status != "canceled") {
				return fmt.Errorf("%w: payment %d target", errInvalidAllocationInput, row.ID)
			}
			// Storno releases cash to FIFO without rewriting either durable
			// explicit preference or historical invoice_id attribution.
			if status == "canceled" {
				row.PreferredInvoiceID = 0
			}
		}
		owners[row.ID] = row.account
		payments = append(payments, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, payment := range payments {
		if payment.ReversalOfID != 0 && owners[payment.ReversalOfID] != payment.account {
			return fmt.Errorf("%w: reversal %d account", errInvalidAllocationInput, payment.ID)
		}
		input := allocationInputFor(inputs, payment.account)
		input.Payments = append(input.Payments, payment.allocationPayment)
	}
	return nil
}

func allocationLedgerInputs(ctx context.Context, tx *sql.Tx, yearID, neighborID int64, inputs map[accountKey]*invoiceAllocationInput) error {
	rows, err := tx.QueryContext(ctx, `SELECT billing_year_id,neighbor_id,SUM(amount) FROM neighbor_ledger
		WHERE NOT voided AND ($1::bigint=0 OR billing_year_id=$1) AND ($2::bigint=0 OR neighbor_id=$2)
		GROUP BY billing_year_id,neighbor_id`, yearID, neighborID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key accountKey
		var amount decimal.Decimal
		if err := rows.Scan(&key.yearID, &key.neighborID, &amount); err != nil {
			return err
		}
		allocationInputFor(inputs, key).Ledger = amount
	}
	return rows.Err()
}
