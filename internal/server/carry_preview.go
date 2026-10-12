package server

import (
	"errors"
	"net/http"

	"github.com/d0linger/treckrr/internal/store"
)

// handleNeighborCarryForwardPreview is a read-only confirmation page. Only the
// existing CSRF-protected POST endpoint can create the transfer postings.
func (s *Server) handleNeighborCarryForwardPreview(w http.ResponseWriter, r *http.Request) {
	neighborID, err := pathID(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	year, ok := s.resolveYear(w, r)
	if !ok {
		return
	}
	neighbor, err := s.store.GetNeighbor(r.Context(), neighborID)
	if err != nil {
		s.notFound(w, r)
		return
	}
	back := neighborPaymentsURL(neighborID, year.ID)
	nextID, err := s.store.BillingYearIDForYear(r.Context(), year.Year+1)
	if errors.Is(err, store.ErrNotFound) {
		s.setFlash(w, r, "error", "Kein Folgejahr angelegt. Bitte zuerst das nächste Abrechnungsjahr anlegen.")
		redirect(w, r, back)
		return
	}
	if err != nil {
		s.serverError(w, "carry preview: next year", err)
		return
	}
	preview, err := s.store.PreviewCarryForward(r.Context(), neighborID, year.ID, nextID)
	switch {
	case errors.Is(err, store.ErrCarryTargetCompleted):
		s.setFlash(w, r, "error", "Das Folgejahr ist abgeschlossen — ein Übertrag dorthin ist nicht möglich.")
		redirect(w, r, back)
		return
	case errors.Is(err, store.ErrNotFound):
		s.setFlash(w, r, "error", "Nachbar muss in beiden Abrechnungsjahren vorhanden sein. Bitte dort zuerst hinzufügen.")
		redirect(w, r, back)
		return
	case err != nil:
		s.serverError(w, "carry preview: balances", err)
		return
	}
	data := s.newPage(w, r, "Jahresübertrag prüfen", "dashboard")
	data["Year"] = year
	data["Neighbor"] = neighbor
	data["NextYear"] = year.Year + 1
	data["NextID"] = nextID
	data["Preview"] = preview
	data["Back"] = back
	s.render(w, r, "carry_forward", data)
}
