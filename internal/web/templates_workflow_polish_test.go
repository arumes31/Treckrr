package web

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

func TestBookingFiltersRevealActiveSecondaryFilters(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"none", "neighbor_id", "task", "direction", "kind", "from", "to", "unit", "voided"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			filter := map[string]string{}
			if field != "none" {
				filter[field] = "active"
			}
			page := execPage(t, "entries", map[string]any{
				"User": models.User{Role: models.RoleViewer}, "Year": models.BillingYear{ID: 1},
				"Filter": filter, "SumCost": decimal.Zero, "Pages": 1,
			})
			details := regexp.MustCompile(`<details[^>]+data-booking-filters[^>]*>`).FindString(page)
			wantOpen := field != "none" && field != "neighbor_id" && field != "task"
			if details == "" || strings.Contains(details, " open") != wantOpen {
				t.Errorf("secondary filters open = %q, want %v", details, wantOpen)
			}
			for _, label := range []string{"Material / Pauschale", "Traktor / Gespann / Gefährt"} {
				if !strings.Contains(page, label) {
					t.Errorf("missing consistent booking label %q", label)
				}
			}
		})
	}
}

func TestBookingSavedViewsPreserveActiveSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query string
		active      bool
	}{
		{name: "inactive", query: "year=1"},
		{name: "active", query: "kind=fixed&year=1", active: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page := execPage(t, "entries", map[string]any{
				"User": models.User{Username: "reader", Role: models.RoleViewer},
				"Year": models.BillingYear{ID: 1}, "Filter": map[string]string{}, "SumCost": decimal.Zero, "Pages": 1,
				"SavedQuery": tc.query,
				"SavedViews": []store.SavedView{{ID: 7, Name: "Material", Query: "kind=fixed&year=1"}},
			})
			details := regexp.MustCompile(`<details[^>]+data-saved-views[^>]*>`).FindString(page)
			if strings.Contains(details, " open") != tc.active {
				t.Errorf("saved views disclosure = %q, want open %v", details, tc.active)
			}
			active := strings.Contains(page, `aria-current="true">Material</a>`)
			if active != tc.active {
				t.Errorf("saved view active = %v, want %v", active, tc.active)
			}
			if !strings.Contains(page, `href="/buchungen?kind=fixed&amp;year=1"`) {
				t.Error("saved view lost its filter destination")
			}
		})
	}
}

func TestBookingBulkActionsRespectReadOnlyRole(t *testing.T) {
	t.Parallel()
	for _, role := range []string{models.RoleViewer, models.RoleEditor, models.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			page := execPage(t, "entries", map[string]any{
				"User": models.User{Role: role}, "Year": models.BillingYear{ID: 1},
				"Filter": map[string]string{}, "SumCost": decimal.NewFromInt(25), "HasEntryRows": true, "Pages": 1, "PhotoCounts": map[int64]int{},
				"Rows": []store.BookingRow{{Source: "entry", Kind: "equipment", EntryRow: store.EntryRow{
					Entry: models.Entry{ID: 3, TaskLabel: "Heuernte", Cost: decimal.NewFromInt(25)},
				}}},
			})
			for _, control := range []string{`action="/buchungen/bulk"`, `name="booking_id"`, `value="void"`, `value="unvoid"`, `value="delete"`} {
				if strings.Contains(page, control) != (role != models.RoleViewer) {
					t.Errorf("role %s: unexpected mutation control %q", role, control)
				}
			}
			for _, content := range []string{"Heuernte", "25,00 €", "Gefilterte Liste als CSV"} {
				if !strings.Contains(page, content) {
					t.Errorf("readable booking data missing %q", content)
				}
			}
		})
	}
}

func TestBatchInvoiceSelectionMetadataAndPermissions(t *testing.T) {
	t.Parallel()
	for _, role := range []string{models.RoleViewer, models.RoleEditor} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			page := execPage(t, "batch_invoice", map[string]any{
				"User": models.User{Role: role}, "Year": models.BillingYear{ID: 1}, "CompanyOK": true,
				"Total": decimal.RequireFromString("12.34"), "Issuable": 1,
				"Rows": []map[string]any{
					{"Neighbor": models.Neighbor{ID: 2, Name: "Bereit"}, "Gross": decimal.RequireFromString("12.34"), "Issuable": true, "ContentHash": "frozen-hash"},
					{"Neighbor": models.Neighbor{ID: 3, Name: "Fehlt"}, "Gross": decimal.NewFromInt(9), "Issuable": false, "Missing": []string{"Adresse"}},
					{"Neighbor": models.Neighbor{ID: 4, Name: "Abgerechnet"}, "Gross": decimal.Zero, "AlreadyInvoiced": true},
				},
			})
			for _, control := range []string{`data-invoice-gross="12.34"`, `name="hash_2" value="frozen-hash"`, `data-batch-submit`} {
				if strings.Contains(page, control) != (role == models.RoleEditor) {
					t.Errorf("role %s: unexpected selection metadata/control %q", role, control)
				}
			}
			for _, absent := range []string{`name="neighbor_id" value="3"`, `name="neighbor_id" value="4"`, "wird festgeschrieben"} {
				if strings.Contains(page, absent) {
					t.Errorf("batch contains ineligible selection or misleading state %q", absent)
				}
			}
			fallback := "Vorschau aller zur Festschreibung bereiten Rechnungen"
			if role == models.RoleEditor {
				fallback = "Summe aller wählbaren Rechnungen"
			} else if strings.Contains(page, "abwählen") || !strings.Contains(page, "data-readonly-notice") {
				t.Error("read-only preview offers selection or lacks its permission explanation")
			}
			if !strings.Contains(page, fallback) || !strings.Contains(page, "12,34 €") {
				t.Error("role-appropriate total fallback missing")
			}
		})
	}
}

func TestMailFiltersExposeCurrentPage(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"", "pending", "sending", "sent", "failed", "ambiguous", "held"} {
		t.Run("status_"+status, func(t *testing.T) {
			t.Parallel()
			page := execPage(t, "mail_outbox", map[string]any{
				"User": models.User{Role: models.RoleAdmin, IsAdmin: true}, "Status": status,
				"MailEnabled": true, "Operations": store.MailOperationsStatus{},
			})
			nav := regexp.MustCompile(`(?s)<nav class="mailfilters".*?</nav>`).FindString(page)
			if strings.Count(nav, `aria-current="page"`) != 1 {
				t.Fatalf("expected one active mail filter: %s", nav)
			}
			href := "/admin/mail"
			if status != "" {
				href += "?status=" + status
			}
			if !strings.Contains(nav, `href="`+href+`" aria-current="page"`) {
				t.Errorf("wrong active filter for %q", status)
			}
		})
	}
}

func TestReadOnlyWorkflowPagesDoNotOfferMutations(t *testing.T) {
	t.Parallel()
	rollover := execPage(t, "year_rollover", map[string]any{
		"User": models.User{Role: models.RoleViewer}, "Source": models.BillingYear{ID: 1, Year: 2026},
		"TargetYear": 2027, "OpenAmount": decimal.Zero, "CreditAmount": decimal.Zero,
	})
	hasNotice := strings.Count(rollover, "data-readonly-notice") == 1 && strings.Contains(rollover, "Nur-Lese-Zugriff")
	if regexp.MustCompile(`<form[^>]+action="/years/`).MatchString(rollover) || !hasNotice {
		t.Error("read-only rollover offers a write action or lacks explanation")
	}
	mail := execPage(t, "mail_outbox", map[string]any{
		"User": models.User{Role: models.RoleViewer}, "Status": "held", "Operations": store.MailOperationsStatus{},
		"Items": []map[string]any{{"ID": 1, "Status": "held", "CreatedAt": time.Now()}},
	})
	if regexp.MustCompile(`<form[^>]+action="/admin/mail/`).MatchString(mail) {
		t.Error("read-only mail page offers a write action")
	}
}

func TestOfflineCopyMatchesBookingQueueScope(t *testing.T) {
	t.Parallel()
	page := execPage(t, "offline", nil)
	for _, text := range []string{"bereits geöffneten Buchungsformular", "Schnellerfassung", "Offline gespeichert", "Formularentwurf ist noch keine Buchung", "benötigen eine Verbindung", "nicht automatisch offline verfügbar"} {
		if !strings.Contains(page, text) {
			t.Errorf("offline guidance missing %q", text)
		}
	}
	if strings.Contains(page, "werden Änderungen gespeichert") {
		t.Error("offline page promises arbitrary edits will sync")
	}
}
