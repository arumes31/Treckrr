package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

// InvoiceSourceCheck is a read-only readiness report, not a source assignment or
// permission to migrate. It contains counts and opaque record IDs, never names,
// invoice text, payment data or amounts. Concurrent changes require a new check.
type InvoiceSourceCheck struct {
	Checked        int                  `json:"checked"`
	Matched        int                  `json:"matched"`
	MatchedEntries int                  `json:"matched_entries"`
	Issues         []InvoiceSourceIssue `json:"issues"`
}

// InvoiceSourceIssue identifies an account requiring manual investigation before
// any future migration can bind historical invoices to live booking sources.
type InvoiceSourceIssue struct {
	InvoiceID  int64  `json:"invoice_id"`
	YearID     int64  `json:"year_id"`
	NeighborID int64  `json:"neighbor_id"`
	Reason     string `json:"reason"`
}

type invoiceSourceSnapshot struct {
	id, yearID, neighborID int64
	net, gross             decimal.NullDecimal
	hash                   string
	lines                  []byte
}

// Pointers distinguish explicit zero values from absent or null snapshot fields.
type invoiceSourceLine struct {
	Date      *time.Time       `json:"date"`
	Label     *string          `json:"label"`
	Unit      *string          `json:"unit"`
	Quantity  *decimal.Decimal `json:"qty"`
	UnitPrice *decimal.Decimal `json:"unit_price"`
	Cost      *decimal.Decimal `json:"cost"`
}

// CheckInvoiceSourceAssignments checks active invoices against their claimed
// sources, or the complete account before assignments exist. It uses a
// read-only repeatable-read transaction: counts, snapshots and source lines come
// from one consistent view, without locks that reserve a future migration. Old
// canceled documents and mutable issuer/recipient master data are not remapped.
func (s *Store) CheckInvoiceSourceAssignments(ctx context.Context) (InvoiceSourceCheck, error) {
	report := InvoiceSourceCheck{Issues: make([]InvoiceSourceIssue, 0)}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return report, fmt.Errorf("invoice source check: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; no-op after Commit
	snapshots, err := readInvoiceSourceSnapshots(ctx, tx)
	if err != nil {
		return report, fmt.Errorf("invoice source check: snapshots: %w", err)
	}
	assigned, err := invoiceSourcesAvailable(ctx, tx)
	if err != nil {
		return report, err
	}
	if assigned {
		byInvoice, assignmentIssues, err := readAssignedInvoiceSources(ctx, tx)
		if err != nil {
			return report, fmt.Errorf("invoice source check: assigned sources: %w", err)
		}
		for _, snapshot := range snapshots {
			report.Checked++
			sources := byInvoice[snapshot.id]
			reason := assignmentIssues[snapshot.id]
			for _, source := range sources {
				if source.NeighborID != snapshot.neighborID || source.BillingYearID != snapshot.yearID {
					reason = "source_account_mismatch"
				}
			}
			if reason == "" {
				reason = invoiceSourceMismatch(snapshot, sources)
			}
			if reason != "" {
				report.Issues = append(report.Issues, InvoiceSourceIssue{
					InvoiceID: snapshot.id, YearID: snapshot.yearID, NeighborID: snapshot.neighborID, Reason: reason,
				})
				continue
			}
			report.Matched++
			report.MatchedEntries += len(sources)
		}
		if err := tx.Commit(); err != nil {
			return report, fmt.Errorf("invoice source check: finish: %w", err)
		}
		return report, nil
	}
	// This preflight must also run before the source-assignment migration.
	// Do not reference invoice_entries, which is intentionally absent then.
	rows, err := tx.QueryContext(ctx, `SELECT `+entryCols+`, false AS invoice_locked FROM entries
		WHERE NOT voided AND EXISTS (
			SELECT 1 FROM invoices i WHERE i.billing_year_id=entries.billing_year_id
			AND i.neighbor_id=entries.neighbor_id AND i.kind='invoice' AND i.status='issued')
		ORDER BY billing_year_id, neighbor_id, entry_date, id`)
	if err != nil {
		return report, fmt.Errorf("invoice source check: sources: %w", err)
	}
	entries, err := collectEntries(rows)
	closeErr := rows.Close()
	if err != nil {
		return report, fmt.Errorf("invoice source check: sources: %w", err)
	}
	if closeErr != nil {
		return report, fmt.Errorf("invoice source check: close sources: %w", closeErr)
	}
	byAccount := make(map[accountKey][]models.Entry)
	for _, entry := range entries {
		key := accountKey{yearID: entry.BillingYearID, neighborID: entry.NeighborID}
		byAccount[key] = append(byAccount[key], entry)
	}
	for _, snapshot := range snapshots {
		report.Checked++
		sources := byAccount[accountKey{yearID: snapshot.yearID, neighborID: snapshot.neighborID}]
		if reason := invoiceSourceMismatch(snapshot, sources); reason != "" {
			report.Issues = append(report.Issues, InvoiceSourceIssue{
				InvoiceID: snapshot.id, YearID: snapshot.yearID,
				NeighborID: snapshot.neighborID, Reason: reason,
			})
			continue
		}
		report.Matched++
		report.MatchedEntries += len(sources)
	}
	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("invoice source check: finish: %w", err)
	}
	return report, nil
}

func readInvoiceSourceSnapshots(ctx context.Context, tx *sql.Tx) ([]invoiceSourceSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, billing_year_id, neighbor_id, net, gross, content_hash, lines
		FROM invoices WHERE kind='invoice' AND status='issued' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []invoiceSourceSnapshot
	for rows.Next() {
		var snapshot invoiceSourceSnapshot
		if err := rows.Scan(&snapshot.id, &snapshot.yearID, &snapshot.neighborID,
			&snapshot.net, &snapshot.gross, &snapshot.hash, &snapshot.lines); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func invoiceSourceMismatch(snapshot invoiceSourceSnapshot, entries []models.Entry) string {
	if !snapshot.net.Valid || !snapshot.gross.Valid || snapshot.hash == "" || len(snapshot.lines) == 0 {
		return "missing_snapshot"
	}
	var lines []invoiceSourceLine
	if err := json.Unmarshal(snapshot.lines, &lines); err != nil || len(lines) == 0 {
		return "invalid_snapshot"
	}
	for _, line := range lines {
		missingDate := line.Date == nil || line.Date.IsZero()
		missingText := line.Label == nil || line.Unit == nil
		missingAmount := line.Quantity == nil || line.UnitPrice == nil || line.Cost == nil
		if missingDate || missingText || missingAmount {
			return "invalid_snapshot"
		}
	}
	// Reuse the issuer's exact source projection, but not current tax settings or
	// parties: those are deliberately irrelevant to a historical source match.
	live := invoiceContentFromEntries(models.Company{}, models.Neighbor{}, entries)
	if len(lines) != len(live.Lines) {
		return "source_count_mismatch"
	}
	for i, line := range lines {
		source := live.Lines[i]
		if line.Date.Format("2006-01-02") != source.Date.Format("2006-01-02") ||
			*line.Label != source.Label || *line.Unit != source.Unit ||
			!line.Quantity.Equal(source.Quantity) || !line.UnitPrice.Equal(source.UnitPrice) ||
			!line.Cost.Equal(source.Cost) {
			return "source_line_mismatch"
		}
	}
	if !snapshot.net.Decimal.Equal(models.RoundMoney(live.Net)) {
		return "source_total_mismatch"
	}
	return ""
}
