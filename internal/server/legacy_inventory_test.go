package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d0linger/treckrr/internal/models"
)

func TestLegacyInventoryRequiresAdmin(t *testing.T) {
	for _, tc := range []struct {
		name string
		user *models.User
	}{
		{name: "anonymous"},
		{name: "viewer", user: &models.User{ID: 1, Username: "viewer", Role: models.RoleViewer}},
		{name: "editor", user: &models.User{ID: 2, Username: "editor", Role: models.RoleEditor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer()
			req := httptest.NewRequest(http.MethodGet, "/admin/legacy-inventory?invoice_sources=1&format=json", nil)
			req = req.WithContext(context.WithValue(req.Context(), userCacheKey, &userCache{user: tc.user, done: true}))
			rr := httptest.NewRecorder()
			// No store is configured: reaching the query would panic instead of
			// merely returning a different non-admin response.
			s.admin(s.handleLegacyInventory).ServeHTTP(rr, req)
			if rr.Code != http.StatusSeeOther && rr.Code != http.StatusForbidden {
				t.Fatalf("non-admin status = %d", rr.Code)
			}
			if strings.Contains(rr.Body.String(), "invoice_sources") {
				t.Fatal("non-admin response exposed invoice source report")
			}
		})
	}
}

func TestInvoiceSourceIssueDescription(t *testing.T) {
	for _, tc := range []struct{ name, reason, want string }{
		{name: "missing snapshot", reason: "missing_snapshot", want: "fehlt oder ist unvollständig"},
		{name: "invalid snapshot", reason: "invalid_snapshot", want: "nicht zuverlässig gelesen"},
		{name: "source count", reason: "source_count_mismatch", want: "Anzahl der Buchungen"},
		{name: "source line", reason: "source_line_mismatch", want: "Mindestens eine Buchung"},
		{name: "source total", reason: "source_total_mismatch", want: "Summe der Buchungen"},
		{name: "unknown reason", reason: "future_reason", want: "Vor einer Migration manuell prüfen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := invoiceSourceIssueDescription(tc.reason); !strings.Contains(got, tc.want) {
				t.Fatalf("description = %q, want %q", got, tc.want)
			}
		})
	}
}
