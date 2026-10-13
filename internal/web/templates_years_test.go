package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

// TestYearsKeepsOnePrimaryActionPerRow prevents administration controls from
// competing with the next workflow action on the year list.
func TestYearsKeepsOnePrimaryActionPerRow(t *testing.T) {
	t.Parallel()
	page := mainContent(t, execPage(t, "years", map[string]any{
		"Rows": []map[string]any{
			{"ID": int64(1), "Year": 2025, "Base": "2025 — Standard", "Entries": 3, "Completed": true},
			{"ID": int64(2), "Year": 2026, "Base": "2025 — Standard", "Entries": 4},
		},
		"Bases":    []models.PriceBase{{ID: 1, Year: 2025, Name: "Standard"}},
		"HasBases": true,
	}))
	for _, action := range []string{
		`href="/years/1/wechsel">Jahreswechsel</a>`,
		`href="/years/2/abschluss">Abschluss prüfen</a>`,
	} {
		if count := strings.Count(page, action); count != 1 {
			t.Errorf("year-row action %q occurs %d times, want 1", action, count)
		}
	}
	for _, want := range []string{"Jahreswechsel", "Abschluss prüfen", "Weitere Aktionen", "Jahresdaten bearbeiten"} {
		if !strings.Contains(page, want) {
			t.Errorf("year list missing %q", want)
		}
	}
	if !strings.Contains(page, `class="work-surface years-workspace"`) {
		t.Error("year management is missing its solid work surface")
	}
}

// TestYearClosingPrioritizesOpenChecks keeps unresolved items above the final
// action while relegating the complete checklist to one optional disclosure.
func TestYearClosingPrioritizesOpenChecks(t *testing.T) {
	t.Parallel()
	page := mainContent(t, execPage(t, "year_closing", map[string]any{
		"Year": map[string]any{"ID": int64(2), "Year": 2026, "Status": "in_progress"},
		"Checks": []store.ClosingCheck{
			{Key: "clean", Label: "Belege", Detail: "Vollständig"},
			{Key: "open", Label: "Zahlungen", Detail: "Beträge prüfen", Count: 2, Amount: decimal.NewFromInt(80)},
		},
		"OpenChecks": 1,
	}))
	for _, want := range []string{"1 Punkt(e) offen", "Zahlungen", "Alle Prüfungen anzeigen", "Jahr 2026 abschließen"} {
		if !strings.Contains(page, want) {
			t.Errorf("closing view missing %q", want)
		}
	}
	if strings.Contains(page, `class="list__row card"`) {
		t.Error("closing checklist still renders every check as a separate card")
	}
	if !strings.Contains(page, `class="work-surface closing-workspace"`) {
		t.Error("year closing is missing its solid work surface")
	}
}

func TestYearClosingAccountRemainderPaymentLinks(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d accounts", count), func(t *testing.T) {
			t.Parallel()
			neighbors := []store.ClosingNeighbor{{ID: 7, Name: "Synthetic farm A"}}
			if count == 2 {
				neighbors = append(neighbors, store.ClosingNeighbor{ID: 8, Name: "Synthetic farm B"})
			}
			page := mainContent(t, execPage(t, "year_closing", map[string]any{
				"Year": map[string]any{"ID": int64(2), "Year": 2026, "Status": "in_progress"},
				"Checks": []store.ClosingCheck{{
					Key: "account_unpaid", Label: "Zusätzliche offene Kontoposten",
					Count: count, Amount: decimal.NewFromInt(10), Neighbors: neighbors,
				}},
				"OpenChecks": 1,
			}))
			for _, neighbor := range neighbors {
				want := fmt.Sprintf(`href="/neighbors/%d?year=2&amp;view=payments"`, neighbor.ID)
				if !strings.Contains(page, want) {
					t.Errorf("account remainder warning does not link to payments: %s", want)
				}
			}
			if !strings.Contains(page, "Zusätzliche offene Kontoposten") || strings.Contains(page, "Alles bereit") {
				t.Error("account remainder is hidden by the clean-year state")
			}
			if !strings.Contains(page, `action="/years/2/status"`) {
				t.Error("an advisory account warning must not remove the year-close action")
			}
		})
	}
}
