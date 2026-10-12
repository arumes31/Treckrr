package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// handleLegacyInventory is an explicit admin-only inspection, not a dashboard
// query. Its aggregate output never includes names, amounts or booking text.
func (s *Server) handleLegacyInventory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	report, err := s.store.ReadLegacyInventory(ctx)
	if err != nil {
		s.serverError(w, "legacy inventory", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(report); err != nil {
			return
		}
		return
	}
	data := s.newPage(w, r, "Historische Datenformate", "backup")
	data["Inventory"] = report
	s.render(w, r, "legacy_inventory", data)
}
