package store

import (
	"context"
	"database/sql"

	"github.com/shopspring/decimal"
)

// ---- Jahresabschluss-Checkliste (Ausbaukarte 59) ---------------------------

// ClosingCheck is one item of the pre-close review: what it looked at, how many
// rows fall through, and the names behind them. Blocking is deliberately not a
// field — the checklist informs the operator, it does not decide for them.
type ClosingCheck struct {
	Key       string // stable id, for the template
	Label     string
	Detail    string   // what "open" means for this check
	Count     int      // 0 = clean
	Names     []string // at most a handful, for the hint line
	Amount    decimal.Decimal
	Neighbors []ClosingNeighbor
}

// ClosingNeighbor identifies an affected account without relying on its name.
type ClosingNeighbor struct {
	ID   int64
	Name string
}

// Clean reports whether nothing is outstanding for this check.
func (c ClosingCheck) Clean() bool { return c.Count == 0 }

// More is how many affected rows the Names sample leaves unnamed, so the
// template can say "… und N weitere" without doing arithmetic.
func (c ClosingCheck) More() int { return c.Count - len(c.Names) }

// closingNamesQuery runs a "name" list query and returns count + the first few names,
// so the checklist can say WHO is affected instead of only how many.
func closingNamesQuery(ctx context.Context, tx *sql.Tx, q string, args ...any) (int, []string, []ClosingNeighbor, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, nil, nil, err
	}
	defer rows.Close()
	var names []string
	var neighbors []ClosingNeighbor
	n := 0
	for rows.Next() {
		var neighbor ClosingNeighbor
		if err := rows.Scan(&neighbor.ID, &neighbor.Name); err != nil {
			return 0, nil, nil, err
		}
		neighbors = append(neighbors, neighbor)
		n++
		if len(names) < 5 {
			names = append(names, neighbor.Name)
		}
	}
	return n, names, neighbors, rows.Err()
}

// YearClosingChecks reviews a billing year before it is closed: who still has
// bookings without a frozen invoice, which invoices were never handed over,
// what is still unpaid, and which Abschläge never got a Schlussrechnung.
func (s *Store) YearClosingChecks(ctx context.Context, yearID int64) ([]ClosingCheck, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	out := make([]ClosingCheck, 0, 5)

	// 1. Bookings but no frozen invoice — the classic "forgot someone".
	n, names, neighbors, err := closingNamesQuery(ctx, tx,
		`SELECT n.id, n.name FROM neighbors n
		  WHERE EXISTS (SELECT 1 FROM entries e
		                 WHERE e.neighbor_id = n.id AND e.billing_year_id = $1 AND NOT e.voided
		                   AND NOT EXISTS(SELECT 1 FROM invoice_entries ie WHERE ie.entry_id=e.id AND ie.active))
		  ORDER BY n.name`, yearID)
	if err != nil {
		return nil, err
	}
	out = append(out, ClosingCheck{
		Key: "uninvoiced", Label: "Buchungen ohne festgeschriebene Rechnung",
		Detail: "Diese Nachbarn haben noch nicht abgerechnete Leistungen im Jahr.",
		Count:  n, Names: names, Neighbors: neighbors,
	})

	// 2. Issued but never handed over (no beleg_send, no mail).
	n, names, neighbors, err = closingNamesQuery(ctx, tx,
		`SELECT DISTINCT n.id, n.name FROM invoices iv JOIN neighbors n ON n.id = iv.neighbor_id
		  WHERE iv.billing_year_id = $1 AND iv.kind = 'invoice' AND iv.status = 'issued'
		    AND NOT EXISTS (SELECT 1 FROM beleg_sends bs
		                     WHERE bs.invoice_id = iv.id AND bs.billing_year_id = iv.billing_year_id
		                       AND bs.neighbor_id = iv.neighbor_id)
		  ORDER BY n.name`, yearID)
	if err != nil {
		return nil, err
	}
	out = append(out, ClosingCheck{
		Key: "unsent", Label: "Rechnungen ohne eindeutigen Versandnachweis",
		Detail: "Kein Versandvermerk für diese konkrete Rechnung. Frühere Vermerke zum Jahreskonto bleiben erhalten.",
		Count:  n, Names: names, Neighbors: neighbors,
	})

	// 3. Per-document remainders share one allocation snapshot with dunning.
	invoices, err := invoiceOpenRows(ctx, tx, yearID, 0)
	if err != nil {
		return nil, err
	}
	open := ClosingCheck{
		Key: "unpaid", Label: "Offene Beträge",
		Detail: "Ausgestellte Rechnungen, die noch nicht vollständig bezahlt sind.",
	}
	documentOpen := make(map[int64]decimal.Decimal)
	for _, invoice := range invoices {
		if !invoice.Open.IsPositive() {
			continue
		}
		open.Count++
		open.Neighbors = append(open.Neighbors, ClosingNeighbor{ID: invoice.NeighborID, Name: invoice.Name})
		open.Amount = open.Amount.Add(invoice.Open)
		documentOpen[invoice.NeighborID] = documentOpen[invoice.NeighborID].Add(invoice.Open)
		if len(open.Names) < 5 {
			open.Names = append(open.Names, invoice.Name)
		}
	}
	out = append(out, open)
	accountOpen, err := closingAccountRemainders(
		ctx,
		tx,
		yearID,
		documentOpen,
	)
	if err != nil {
		return nil, err
	}
	out = append(out, accountOpen)

	// 4. Abschläge that never became a Schlussrechnung (Ausbaukarte 54): money
	// was requested on account and the final document is missing.
	n, names, neighbors, err = closingNamesQuery(ctx, tx,
		`SELECT DISTINCT n.id, n.name FROM invoices a JOIN neighbors n ON n.id = a.neighbor_id
		  WHERE a.billing_year_id = $1 AND a.kind = 'anzahlung' AND a.status = 'issued'
		    AND NOT EXISTS (SELECT 1 FROM invoices iv
		                     WHERE iv.neighbor_id = a.neighbor_id AND iv.billing_year_id = a.billing_year_id
		                       AND iv.kind = 'invoice' AND iv.status = 'issued')
		  ORDER BY n.name`, yearID)
	if err != nil {
		return nil, err
	}
	out = append(out, ClosingCheck{
		Key: "openanzahlung", Label: "Abschläge ohne Schlussrechnung",
		Detail: "Es wurde eine Anzahlung angefordert, aber nie endabgerechnet.",
		Count:  n, Names: names, Neighbors: neighbors,
	})
	return out, tx.Commit()
}

// closingAccountRemainders keeps ledger debits and uninvoiced work visible even
// when all documents are paid. Only the amount beyond open documents is shown,
// once per account, using the same snapshot and balance formula as the dashboard.
func closingAccountRemainders(
	ctx context.Context,
	tx *sql.Tx,
	yearID int64,
	documentOpen map[int64]decimal.Decimal,
) (ClosingCheck, error) {
	check := ClosingCheck{
		Key: "account_unpaid", Label: "Zusätzliche offene Kontoposten",
		Detail: "Offener Jahreskontorest über die offenen Rechnungsbeträge hinaus, " +
			"etwa aus Kontobuchungen oder noch nicht abgerechneten Leistungen.",
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.name,ROUND(balance.payable-balance.paid,2)
		FROM billing_year_neighbors membership JOIN neighbors n ON n.id=membership.neighbor_id
		CROSS JOIN LATERAL (`+perNeighborPayablePaid+`
			WHERE byn.billing_year_id=membership.billing_year_id AND byn.neighbor_id=membership.neighbor_id
		) balance
		WHERE membership.billing_year_id=$1 ORDER BY n.name,n.id`, yearID)
	if err != nil {
		return ClosingCheck{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var neighbor ClosingNeighbor
		var annual decimal.Decimal
		if err := rows.Scan(&neighbor.ID, &neighbor.Name, &annual); err != nil {
			return ClosingCheck{}, err
		}
		residual := annual.Sub(documentOpen[neighbor.ID])
		if !residual.IsPositive() {
			continue
		}
		check.Count++
		check.Amount = check.Amount.Add(residual)
		check.Neighbors = append(check.Neighbors, neighbor)
		if len(check.Names) < 5 {
			check.Names = append(check.Names, neighbor.Name)
		}
	}
	return check, rows.Err()
}

// OpenClosingChecks counts the checks that are not clean.
func OpenClosingChecks(checks []ClosingCheck) int {
	n := 0
	for _, c := range checks {
		if !c.Clean() {
			n++
		}
	}
	return n
}
