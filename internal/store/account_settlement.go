package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

// PaymentUse identifies the current applied portion, which can differ from the
// durable historical invoice reference displayed by older clients.
type PaymentUse struct {
	InvoiceID     int64
	InvoiceNumber string
	Amount        decimal.Decimal
}

type PaymentUsage struct {
	Parts       []PaymentUse
	Unallocated decimal.Decimal
	Reversed    bool
}

// AccountSettlement is a display-only snapshot. Public consumers select one
// invoice's Lines only; the annual reconciliation and payment map stay private.
type AccountSettlement struct {
	InvoiceAccountReconciliation
	Lines          map[int64][]models.SettlementLine
	Payments       map[int64]PaymentUsage
	InvoiceNumbers map[int64]string
}

type settlementSource struct {
	id, referenceID int64
	kind, number    string
	label           string
	date            time.Time
	amount          decimal.Decimal
}

type settlementPayment struct {
	id, reversalID int64
	date           time.Time
	amount         decimal.Decimal
	method         string
}

// ReadAccountSettlement enriches the authoritative allocation in one read-only
// snapshot. It does not write preferences, assign sources or change balances.
func (s *Store) ReadAccountSettlement(ctx context.Context, yearID, neighborID int64) (AccountSettlement, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return AccountSettlement{}, fmt.Errorf("settlement detail: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM billing_year_neighbors
		WHERE billing_year_id=$1 AND neighbor_id=$2)`, yearID, neighborID).Scan(&exists); err != nil {
		return AccountSettlement{}, fmt.Errorf("settlement detail: membership: %w", err)
	}
	if !exists {
		return AccountSettlement{}, ErrNotFound
	}
	accounts, err := invoiceAllocationsTx(ctx, tx, yearID, neighborID)
	if err != nil {
		return AccountSettlement{}, err
	}
	summary := accounts[accountKey{yearID: yearID, neighborID: neighborID}]
	sources, err := settlementSources(ctx, tx, yearID, neighborID)
	if err != nil {
		return AccountSettlement{}, fmt.Errorf("settlement detail: sources: %w", err)
	}
	payments, err := settlementPayments(ctx, tx, yearID, neighborID)
	if err != nil {
		return AccountSettlement{}, fmt.Errorf("settlement detail: payments: %w", err)
	}
	result, err := explainAccountSettlement(summary, sources, payments)
	if err != nil {
		return AccountSettlement{}, err
	}
	result.AnnualRemaining, err = remainingLocked(ctx, tx, yearID, neighborID)
	if err != nil {
		return AccountSettlement{}, fmt.Errorf("settlement detail: account balance: %w", err)
	}
	result.OutsideInvoices = result.AnnualRemaining.Sub(result.OpenInvoices)
	if err := tx.Commit(); err != nil {
		return AccountSettlement{}, fmt.Errorf("settlement detail: finish: %w", err)
	}
	return result, nil
}

// settlementSources loads issued invoices and credit notes plus non-voided
// ledger postings of one account, oldest first.
func settlementSources(ctx context.Context, tx *sql.Tx, yearID, neighborID int64) ([]settlementSource, error) {
	// Private descriptions/notes are excluded. Only structured task labels may
	// describe an unambiguously applied counterservice in a public supplement.
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(references_invoice_id,0),kind,number,issued_on,gross
		FROM invoices WHERE billing_year_id=$1 AND neighbor_id=$2 AND status='issued'
		AND kind IN ('invoice','gutschrift') ORDER BY issued_on,id`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	sources := []settlementSource{}
	for rows.Next() {
		var source settlementSource
		if err := rows.Scan(&source.id, &source.referenceID, &source.kind, &source.number, &source.date, &source.amount); err != nil {
			_ = rows.Close()
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,posting_date,amount,COALESCE(booking->>'task_label','') FROM neighbor_ledger
		WHERE billing_year_id=$1 AND neighbor_id=$2 AND NOT voided ORDER BY posting_date,id`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		source := settlementSource{kind: "ledger"}
		if err := rows.Scan(&source.id, &source.date, &source.amount, &source.label); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// settlementPayments loads the account's non-deleted payments and reversals,
// oldest first.
func settlementPayments(ctx context.Context, tx *sql.Tx, yearID, neighborID int64) ([]settlementPayment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,paid_on,amount,method,COALESCE(reversal_of_payment_id,0)
		FROM payments WHERE billing_year_id=$1 AND neighbor_id=$2 AND deleted_at IS NULL
		ORDER BY paid_on,id`, yearID, neighborID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	payments := []settlementPayment{}
	for rows.Next() {
		var payment settlementPayment
		if err := rows.Scan(&payment.id, &payment.date, &payment.amount, &payment.method, &payment.reversalID); err != nil {
			return nil, err
		}
		payments = append(payments, payment)
	}
	return payments, rows.Err()
}

// explainAccountSettlement itemizes each invoice's applied reduction from the
// allocation summary: attached credits, account offsets and payment parts. It
// also records how each payment was used. It never reallocates money; an
// explanation that does not match the allocation is an error.
func explainAccountSettlement(summary InvoiceAllocationSummary, sources []settlementSource, payments []settlementPayment) (AccountSettlement, error) {
	result := AccountSettlement{
		InvoiceAccountReconciliation: InvoiceAccountReconciliation{Allocations: summary},
		Lines:                        make(map[int64][]models.SettlementLine), Payments: make(map[int64]PaymentUsage),
	}
	numbers := make(map[int64]string)
	attached := make(map[int64][]models.SettlementLine)
	pool := []models.SettlementLine{}
	ledger := []models.SettlementLine{}
	ledgerNet := decimal.Zero
	hasLedgerDebit := false
	for _, source := range sources {
		if source.kind == "ledger" {
			ledgerNet = ledgerNet.Add(source.amount)
			hasLedgerDebit = hasLedgerDebit || source.amount.IsPositive()
			if source.amount.IsNegative() {
				label := "Gegenleistung / Kontoverrechnung"
				if task := strings.TrimSpace(source.label); task != "" {
					label = "Gegenleistung Nachbar: " + task
				}
				ledger = append(ledger, models.SettlementLine{Date: source.date, Kind: "offset",
					Label: label, Amount: source.amount.Neg()})
			}
			continue
		}
		numbers[source.id] = source.number
		if !source.amount.IsNegative() {
			continue
		}
		line := models.SettlementLine{Date: source.date, Kind: "credit", Label: "Gutschrift " + source.number, Amount: source.amount.Neg()}
		if source.kind == "invoice" {
			line.Kind, line.Label = "offset", "Historischer Rechnungsüberschuss "+source.number
		}
		if source.referenceID != 0 {
			attached[source.referenceID] = append(attached[source.referenceID], line)
		} else {
			pool = append(pool, line)
		}
	}
	if ledgerNet.IsNegative() {
		if hasLedgerDebit {
			pool = append(pool, models.SettlementLine{Kind: "offset", Label: "Verrechnung aus Jahreskonto (zusammengefasster Saldo)", Amount: ledgerNet.Neg()})
		} else {
			pool = append(pool, ledger...)
		}
	}
	byPayment := make(map[int64]settlementPayment)
	for _, payment := range payments {
		byPayment[payment.id] = payment
		result.Payments[payment.id] = PaymentUsage{Parts: []PaymentUse{}, Unallocated: decimal.Max(payment.amount, decimal.Zero)}
	}
	for _, payment := range payments {
		if payment.reversalID != 0 {
			result.Payments[payment.id] = PaymentUsage{Reversed: true}
			result.Payments[payment.reversalID] = PaymentUsage{Reversed: true}
		}
	}
	poolTotal := decimal.Zero
	for _, line := range pool {
		poolTotal = poolTotal.Add(line.Amount)
	}
	for _, allocation := range summary.Invoices {
		lines := append([]models.SettlementLine{}, attached[allocation.InvoiceID]...)
		if allocation.OffsetApplied.IsPositive() {
			switch {
			case allocation.OffsetApplied.Equal(poolTotal):
				lines = append(lines, pool...)
			case len(pool) == 1:
				line := pool[0]
				line.Amount = allocation.OffsetApplied
				lines = append(lines, line)
			default:
				lines = append(lines, models.SettlementLine{Kind: "offset",
					Label: "Verrechnung aus Jahreskonto (Einzelzuordnung nicht eindeutig)", Amount: allocation.OffsetApplied})
			}
		}
		for _, part := range allocation.PaymentParts {
			payment, ok := byPayment[part.PaymentID]
			if !ok {
				return AccountSettlement{}, fmt.Errorf("settlement detail: payment metadata missing")
			}
			label := "Zahlung"
			switch strings.ToLower(strings.TrimSpace(payment.method)) {
			case "bar":
				label += " (bar)"
			case "bank", "überweisung":
				label += " (Überweisung)"
			case "verrechnung":
				label += " (Verrechnung)"
			}
			lines = append(lines, models.SettlementLine{Date: payment.date, Kind: "payment", Label: label, Amount: part.Amount})
			usage := result.Payments[part.PaymentID]
			usage.Parts = append(usage.Parts, PaymentUse{InvoiceID: allocation.InvoiceID, InvoiceNumber: numbers[allocation.InvoiceID], Amount: part.Amount})
			usage.Unallocated = usage.Unallocated.Sub(part.Amount)
			result.Payments[part.PaymentID] = usage
		}
		applied := decimal.Max(allocation.Gross, decimal.Zero).Sub(allocation.Remaining)
		validated, err := models.SettlementLines(lines, applied)
		if err != nil {
			return AccountSettlement{}, fmt.Errorf("settlement detail: invoice %d: %w", allocation.InvoiceID, err)
		}
		result.Lines[allocation.InvoiceID] = validated
		result.OpenInvoices = result.OpenInvoices.Add(allocation.Remaining)
	}
	result.InvoiceNumbers = numbers
	return result, nil
}
