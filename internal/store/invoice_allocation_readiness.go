package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/shopspring/decimal"
)

// InvoiceAllocationReadiness reports whether current financial inputs satisfy
// the allocation planner's contract. It is not source-assignment verification,
// a migration authorization, or a change to the current balance calculation.
type InvoiceAllocationReadiness struct {
	CheckedAccounts int                      `json:"checked_accounts"`
	ReadyAccounts   int                      `json:"ready_accounts"`
	Issues          []InvoiceAllocationIssue `json:"issues"`
}

// InvoiceAllocationIssue contains only opaque identifiers and a stable reason.
// RecordID is zero for an aggregate ledger issue. Legacy precision is reported
// as unsupported input, not repaired or classified as damaged financial data.
type InvoiceAllocationIssue struct {
	YearID     int64  `json:"year_id"`
	NeighborID int64  `json:"neighbor_id"`
	Entity     string `json:"entity"`
	RecordID   int64  `json:"record_id"`
	Reason     string `json:"reason"`
}

type readinessInvoice struct {
	allocationInvoice
	account     accountKey
	kind        string
	referenceID int64
	hasGross    bool
}

type readinessPayment struct {
	allocationPayment
	account accountKey
}

type readinessLedger struct {
	account accountKey
	net     decimal.Decimal
}

// CheckInvoiceAllocationReadiness reads existing invoice, payment and ledger
// records in one consistent snapshot. It never writes, takes mutation locks,
// returns monetary values, or changes CheckInvoiceSourceAssignments semantics.
// Historical payments.invoice_id is deliberately not a payment preference.
// Only active inputs are checked: soft-deleted payments and voided ledger rows
// need separate migration validation before they may later be restored.
// This prerequisite is not called by runtime financial workflows.
func (s *Store) CheckInvoiceAllocationReadiness(ctx context.Context) (InvoiceAllocationReadiness, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return InvoiceAllocationReadiness{}, fmt.Errorf("allocation readiness: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	invoices, err := readAllocationReadinessInvoices(ctx, tx)
	if err != nil {
		return InvoiceAllocationReadiness{}, fmt.Errorf("allocation readiness: invoices: %w", err)
	}
	payments, err := readAllocationReadinessPayments(ctx, tx)
	if err != nil {
		return InvoiceAllocationReadiness{}, fmt.Errorf("allocation readiness: payments: %w", err)
	}
	ledgers, err := readAllocationReadinessLedger(ctx, tx)
	if err != nil {
		return InvoiceAllocationReadiness{}, fmt.Errorf("allocation readiness: ledger: %w", err)
	}
	report := allocationReadiness(invoices, payments, ledgers)
	if err := tx.Commit(); err != nil {
		return InvoiceAllocationReadiness{}, fmt.Errorf("allocation readiness: finish: %w", err)
	}
	return report, nil
}

func readAllocationReadinessInvoices(ctx context.Context, tx *sql.Tx) ([]readinessInvoice, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, billing_year_id, neighbor_id, kind, issued_on, gross, COALESCE(references_invoice_id,0)
		  FROM invoices WHERE status='issued' AND kind IN ('invoice','gutschrift') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []readinessInvoice
	for rows.Next() {
		var row readinessInvoice
		var gross decimal.NullDecimal
		if err := rows.Scan(&row.ID, &row.account.yearID, &row.account.neighborID,
			&row.kind, &row.IssuedOn, &gross, &row.referenceID); err != nil {
			return nil, err
		}
		row.Gross, row.hasGross = gross.Decimal, gross.Valid
		out = append(out, row)
	}
	return out, rows.Err()
}

func readAllocationReadinessPayments(ctx context.Context, tx *sql.Tx) ([]readinessPayment, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, billing_year_id, neighbor_id, amount, paid_on, COALESCE(reversal_of_payment_id,0)
		  FROM payments WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []readinessPayment
	for rows.Next() {
		var row readinessPayment
		if err := rows.Scan(&row.ID, &row.account.yearID, &row.account.neighborID,
			&row.Amount, &row.PaidOn, &row.ReversalOfID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func readAllocationReadinessLedger(ctx context.Context, tx *sql.Tx) ([]readinessLedger, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT billing_year_id, neighbor_id, SUM(amount)
		  FROM neighbor_ledger WHERE NOT voided
		 GROUP BY billing_year_id, neighbor_id ORDER BY billing_year_id, neighbor_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []readinessLedger
	for rows.Next() {
		var row readinessLedger
		if err := rows.Scan(&row.account.yearID, &row.account.neighborID, &row.net); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// allocationReadiness builds account-isolated planner inputs without rounding
// historical values. Relationship validation happens before netting, so equal
// amounts cannot conceal a cross-account credit or payment reversal.
func allocationReadiness(invoices []readinessInvoice, payments []readinessPayment, ledgers []readinessLedger) InvoiceAllocationReadiness {
	report := InvoiceAllocationReadiness{Issues: make([]InvoiceAllocationIssue, 0)}
	inputs := make(map[accountKey]*invoiceAllocationInput)
	inputFor := func(key accountKey) *invoiceAllocationInput {
		if inputs[key] == nil {
			inputs[key] = &invoiceAllocationInput{}
		}
		return inputs[key]
	}
	invalid := make(map[accountKey]bool)
	issue := func(key accountKey, entity string, id int64, reason string) {
		invalid[key] = true
		report.Issues = append(report.Issues, InvoiceAllocationIssue{
			YearID: key.yearID, NeighborID: key.neighborID, Entity: entity, RecordID: id, Reason: reason,
		})
	}
	targets := make(map[int64]readinessInvoice)
	credits := make(map[int64]decimal.Decimal)
	for _, invoice := range invoices {
		inputFor(invoice.account)
		if !invoice.hasGross {
			issue(invoice.account, "invoice", invoice.ID, "missing_frozen_gross")
		}
		if invoice.IssuedOn.IsZero() {
			issue(invoice.account, "invoice", invoice.ID, "invalid_document_date")
		}
		if invoice.kind == "invoice" {
			targets[invoice.ID] = invoice
		}
	}
	for _, invoice := range invoices {
		if invoice.kind != "gutschrift" {
			continue
		}
		if invoice.Gross.IsPositive() || !wholeAllocationCents(invoice.Gross) {
			issue(invoice.account, "invoice", invoice.ID, "invalid_credit_amount")
		}
		if invoice.referenceID == 0 {
			input := inputFor(invoice.account)
			input.StandaloneCredits = input.StandaloneCredits.Add(invoice.Gross)
			continue
		}
		target, ok := targets[invoice.referenceID]
		switch {
		case !ok:
			issue(invoice.account, "invoice", invoice.ID, "credit_target_unavailable")
		case target.account != invoice.account:
			issue(invoice.account, "invoice", invoice.ID, "credit_target_account_mismatch")
		default:
			credits[target.ID] = credits[target.ID].Add(invoice.Gross)
		}
	}
	for _, invoice := range invoices {
		if invoice.kind != "invoice" {
			continue
		}
		claim := invoice.allocationInvoice
		claim.AttachedCredit = credits[invoice.ID]
		if claim.AttachedCredit.Neg().GreaterThan(decimal.Max(claim.Gross, decimal.Zero)) {
			issue(invoice.account, "invoice", invoice.ID, "attached_credit_exceeds_gross")
		}
		input := inputFor(invoice.account)
		input.Invoices = append(input.Invoices, claim)
	}
	byPayment := make(map[int64]readinessPayment)
	reversalCounts := make(map[int64]int)
	for _, payment := range payments {
		byPayment[payment.ID] = payment
		if payment.ReversalOfID != 0 {
			reversalCounts[payment.ReversalOfID]++
		}
	}
	for _, payment := range payments {
		input := inputFor(payment.account)
		input.Payments = append(input.Payments, payment.allocationPayment)
		if !wholeAllocationCents(payment.Amount) {
			issue(payment.account, "payment", payment.ID, "legacy_fractional_payment")
		}
		if payment.PaidOn.IsZero() {
			issue(payment.account, "payment", payment.ID, "invalid_payment_date")
		}
		if payment.ReversalOfID == 0 {
			if !payment.Amount.IsPositive() {
				issue(payment.account, "payment", payment.ID, "invalid_payment_sign")
			}
			continue
		}
		original, ok := byPayment[payment.ReversalOfID]
		switch {
		case !ok:
			issue(payment.account, "payment", payment.ID, "reversal_target_unavailable")
		case original.account != payment.account:
			issue(payment.account, "payment", payment.ID, "reversal_target_account_mismatch")
		case !payment.Amount.IsNegative() || original.ReversalOfID != 0 || !original.Amount.Equal(payment.Amount.Neg()):
			issue(payment.account, "payment", payment.ID, "invalid_reversal")
		case reversalCounts[payment.ReversalOfID] > 1:
			issue(payment.account, "payment", payment.ID, "duplicate_reversal")
		}
	}
	for _, ledger := range ledgers {
		inputFor(ledger.account).Ledger = ledger.net
		if !wholeAllocationCents(ledger.net) {
			issue(ledger.account, "ledger", 0, "legacy_fractional_ledger_net")
		}
	}
	keys := make([]accountKey, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	for _, key := range sortedUniqueAccounts(keys) {
		report.CheckedAccounts++
		if invalid[key] {
			continue
		}
		if _, err := planInvoiceAllocation(*inputs[key]); err != nil {
			issue(key, "account", 0, "planner_input_invalid")
			continue
		}
		report.ReadyAccounts++
	}
	return report
}
