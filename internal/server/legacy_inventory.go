package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/d0linger/treckrr/internal/store"
)

// handleLegacyInventory is an explicit admin-only inspection, not a dashboard
// query. Output includes only aggregate counts and optional internal IDs, never
// names, amounts or booking text.
func (s *Server) handleLegacyInventory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	report, err := s.store.ReadLegacyInventory(ctx)
	if err != nil {
		s.serverError(w, "legacy inventory", err)
		return
	}
	var invoiceSources *store.InvoiceSourceCheck
	if r.URL.Query().Get("invoice_sources") == "1" {
		check, err := s.store.CheckInvoiceSourceAssignments(ctx)
		if err != nil {
			s.serverError(w, "invoice source inventory", err)
			return
		}
		invoiceSources = &check
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		var output any = report
		if invoiceSources != nil {
			output = struct {
				Inventory      store.LegacyInventory     `json:"inventory"`
				InvoiceSources *store.InvoiceSourceCheck `json:"invoice_sources"`
			}{Inventory: report, InvoiceSources: invoiceSources}
		}
		if err := json.NewEncoder(w).Encode(output); err != nil {
			return
		}
		return
	}
	data := s.newPage(w, r, "Historische Datenformate", "backup")
	data["Inventory"] = report
	data["InvoiceSourceCheck"] = invoiceSources
	if invoiceSources != nil {
		issues := make([]invoiceSourceIssueView, 0, len(invoiceSources.Issues))
		for _, issue := range invoiceSources.Issues {
			issues = append(issues, invoiceSourceIssueView{
				InvoiceSourceIssue: issue, Description: invoiceSourceIssueDescription(issue.Reason),
			})
		}
		data["InvoiceSourceIssues"] = issues
	}
	s.render(w, r, "legacy_inventory", data)
}

type invoiceSourceIssueView struct {
	store.InvoiceSourceIssue
	Description string
}

func invoiceSourceIssueDescription(reason string) string {
	switch reason {
	case "missing_snapshot":
		return "Der gespeicherte Rechnungssnapshot fehlt oder ist unvollständig."
	case "invalid_snapshot":
		return "Die gespeicherten Rechnungspositionen können nicht zuverlässig gelesen werden."
	case "source_count_mismatch":
		return "Die Anzahl der Buchungen stimmt nicht mit den gespeicherten Rechnungspositionen überein."
	case "source_line_mismatch":
		return "Mindestens eine Buchung stimmt nicht mit der gespeicherten Rechnungsposition überein."
	case "source_total_mismatch":
		return "Die Summe der Buchungen stimmt nicht mit dem gespeicherten Rechnungsbetrag überein."
	default:
		return "Die Zuordnung ist nicht eindeutig prüfbar. Vor einer Migration manuell prüfen."
	}
}
