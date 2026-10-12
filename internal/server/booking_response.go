package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// bookingSuccess answers the progressive form only after storage succeeded.
// Validation redirects deliberately do not carry this acknowledgement, so a
// browser cannot discard an unsaved draft just because it received a redirect.
func (s *Server) bookingSuccess(w http.ResponseWriter, r *http.Request, message string) {
	neighborID, yearID := formInt64(r, "neighbor_id"), formInt64(r, "year_id")
	s.setFlash(w, r, "success", message)
	if r.Header.Get("X-Booking-Response") == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Saved bool `json:"saved"`
		}{true})
		return
	}
	target := neighborBookingsURL(neighborID, yearID)
	if trimmed(r, "continue_booking") == "1" || trimmed(r, "continue_next") == "1" {
		target = neighborBookingURL(neighborID, yearID)
		next := bookingContinuation(trimmed(r, "booking_kind"), trimmed(r, "booking_direction"), trimmed(r, "entry_date"))
		if len(next) > 0 {
			target += "&" + next.Encode()
		}
	}
	redirect(w, r, target)
}

// bookingContinuation permits only non-financial defaults for the next form.
func bookingContinuation(kind, direction, date string) url.Values {
	next := make(url.Values)
	switch kind {
	case "equipment", "labor", "quantity", "fixed":
		next.Set("next_kind", kind)
	}
	switch direction {
	case "in", "out":
		next.Set("next_direction", direction)
	}
	if parsed, err := time.Parse(time.DateOnly, date); err == nil && parsed.Format(time.DateOnly) == date {
		next.Set("next_date", date)
	}
	return next
}
