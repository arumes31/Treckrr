package web

import (
	"bytes"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
)

func accountPolishContent(t *testing.T, page string, data map[string]any) string {
	t.Helper()
	pages, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := pages[page].ExecuteTemplate(&out, "content", data); err != nil {
		t.Fatalf("render %s: %v", page, err)
	}
	return out.String()
}

func accountPolishData(user models.User, section string) map[string]any {
	d := decimal.RequireFromString
	date := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	return map[string]any{
		"User": user, "Section": section,
		"Base": models.PriceBase{ID: 1}, "Year": models.BillingYear{ID: 1, Year: 2026},
		"Neighbor": models.Neighbor{ID: 2, Name: "Testhof"}, "Today": "2026-10-15",
		"TotalCost": d("322.20"), "LedgerSum": d("-44"), "PaidSum": d("100"),
		"Remaining": d("178.20"), "CreditAmount": decimal.Zero, "Saldo": d("278.20"),
		"TotalHours": d("3"), "BookingCount": 2, "StaleCount": 1,
		"HasTravelRates": true, "TravelFlat": d("5"), "TravelPerKm": decimal.Zero,
		"Entries":   []models.Entry{{ID: 7, Date: date, Unit: "h", Cost: d("322.20")}},
		"PairLabel": map[int64]string{}, "Stale": map[int64]bool{},
		"PhotoCounts": map[int64]int{}, "LinkedFrom": map[int64]int64{}, "LedgerPhotoCounts": map[int64]int{},
		"Ledger":       []map[string]any{{"ID": int64(8), "Date": date, "Amount": d("-44"), "Description": "Gegenleistung"}},
		"Payments":     []map[string]any{{"ID": int64(9), "PaidOn": date, "Amount": d("100"), "Note": "Anzahlung"}},
		"PaymentUsage": map[int64]any{}, "PaymentAmount": d("178.20"),
		"Installments": []map[string]any{{"Plan": map[string]any{"ID": int64(10), "DueOn": date, "Amount": d("50")}, "Status": "offen"}},
	}
}

func TestNeighborRoleControls(t *testing.T) {
	t.Parallel()
	for _, user := range []models.User{
		{Role: models.RoleViewer}, {Role: models.RoleEditor}, {Role: models.RoleAdmin},
		{Role: models.RoleAdmin, Disabled: true}, {Role: "unknown"},
	} {
		for _, section := range []string{"overview", "booking", "bookings", "payments"} {
			name := user.Role + "/" + section
			if user.Disabled {
				name = "disabled/" + section
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				page := accountPolishContent(t, "neighbor", accountPolishData(user, section))
				for _, want := range []string{`/neighbors/2/beleg?year=1`, `/export/neighbor/2?year=1`, "178,20", "322,20"} {
					if !strings.Contains(page, want) {
						t.Errorf("missing readable account data/export %q", want)
					}
				}
				if !user.CanWrite() {
					for _, forbidden := range []string{`method="post"`, `view=booking"`, `/recalc?`, `/carry-forward?`, `/payments/9/edit`, `/entries/7/edit`, `data-entry-form`} {
						if strings.Contains(page, forbidden) {
							t.Errorf("read-only account exposes mutation control %q", forbidden)
						}
					}
				} else if section != "overview" && !strings.Contains(page, `method="post"`) {
					t.Fatal("writer no longer receives the expected form")
				}
			})
		}
	}
}

func TestNeighborSummaryDetailsOnlyOnBookingView(t *testing.T) {
	t.Parallel()
	for _, section := range []string{"overview", "booking", "bookings", "payments"} {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			page := accountPolishContent(t, "neighbor", accountPolishData(models.User{Role: models.RoleEditor}, section))
			header := regexp.MustCompile(`(?s)<header class="summary-card.*?</header>`).FindString(page)
			compact := section == "booking"
			if strings.Contains(header, `summary-card--booking`) != compact || strings.Contains(header, `<details`) != compact {
				t.Fatalf("compact account disclosure does not match section %q", section)
			}
			for _, value := range []string{"Testhof", "2026", "178,20", "322,20", "-44,00", "100,00", "3 h", "2 Positionen", "CSV Export", "Beleg"} {
				if !strings.Contains(html.UnescapeString(header), value) {
					t.Errorf("summary lost existing value/action %q", value)
				}
			}
			if compact && regexp.MustCompile(`<details[^>]*\bopen\b`).MatchString(header) {
				t.Fatal("booking account details must start collapsed")
			}
		})
	}
}

func TestReadOnlyBookingRetainsSnapshotDetails(t *testing.T) {
	t.Parallel()
	d := decimal.RequireFromString
	date := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, page string
		incoming   bool
	}{
		{name: "own service", page: "entry_edit"},
		{name: "neighbor service", page: "ledger_edit", incoming: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := accountPolishData(models.User{Role: models.RoleViewer}, "")
			data["BookingEdit"] = true
			data["BookingPeople"] = []models.BookingPerson{{Name: "Helfer <Alt>", Hours: d("3"), Rate: d("28")}}
			data["BookingValues"] = map[string]string{"booking_kind": "equipment", "booking_direction": "out"}
			data["Entry"] = models.Entry{Date: date, TaskLabel: "Feldarbeit", Note: "Originalnotiz", Unit: "h", Hours: d("3"), HourlyRate: d("64.40"), Cost: d("193.20")}
			delete(data, "Ledger")
			if tc.incoming {
				delete(data, "Entry")
				data["BookingValues"] = map[string]string{"booking_kind": "equipment", "booking_direction": "in"}
				data["Ledger"] = models.LedgerEntry{Date: date, Amount: d("-277.20"), Booking: &models.LedgerBooking{Kind: "equipment", TaskLabel: "Feldarbeit", Note: "Originalnotiz", Unit: "h", Quantity: d("3"), UnitPrice: d("64.40")}}
			}
			page := accountPolishContent(t, tc.page, data)
			for _, want := range []string{"Buchungsdetails", "Feldarbeit", "Originalnotiz", "15.10.2026", "193,20", "64,40", "Helfer &lt;Alt&gt;", "84,00"} {
				if !strings.Contains(page, want) {
					t.Errorf("read-only booking lost snapshot detail %q", want)
				}
			}
			for _, forbidden := range []string{`<form`, `data-entry-form`, `Buchung speichern`, `Serie einrichten`} {
				if strings.Contains(page, forbidden) {
					t.Errorf("read-only snapshot exposes %q", forbidden)
				}
			}
		})
	}
}

func TestClosedAccountKeepsPaymentFlowForWriters(t *testing.T) {
	t.Parallel()
	user := models.User{Role: models.RoleEditor}
	data := accountPolishData(user, "booking")
	data["Completed"] = true
	booking := accountPolishContent(t, "neighbor", data)
	if strings.Contains(booking, `data-entry-form`) || strings.Contains(booking, `data-quick-form`) {
		t.Fatal("closed year must not expose booking forms")
	}
	data["Section"] = "payments"
	payments := accountPolishContent(t, "neighbor", data)
	for _, want := range []string{`action="/neighbors/2/payments"`, `formaction="/years/mark-paid"`, `value="178.20"`} {
		if !strings.Contains(payments, want) {
			t.Errorf("closed account lost payment flow %q", want)
		}
	}
}

func invoicePolishData() map[string]any {
	d := decimal.RequireFromString
	date := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	return map[string]any{
		"User":     models.User{Role: models.RoleEditor},
		"Neighbor": models.Neighbor{ID: 2, Name: "Testhof"}, "Year": models.BillingYear{ID: 1, Year: 2026},
		"Invoice": models.Invoice{ID: 3, Number: "2026-003", IssuedOn: date, Content: &models.InvoiceContent{
			Issuer: models.InvoiceParty{Name: "Aussteller"}, Recipient: models.InvoiceParty{Name: "Testhof"},
			ServiceFrom: date, ServiceTo: date, Net: d("322.20"), Gross: d("322.20"),
			TaxNote: "Unveränderter Steuerhinweis", Hash: "frozen-hash",
			Lines: []models.InvoiceLine{{Date: date, Label: "Feldarbeit", Quantity: d("3"), Unit: "h", UnitPrice: d("92.40"), Cost: d("277.20")},
				{Date: date, Label: "Material", Quantity: d("1"), Unit: "Pauschale", UnitPrice: d("45"), Cost: d("45")}},
		}},
		"Doc":         map[string]any{"Active": true, "AppliedSettlement": d("144"), "Remaining": d("178.20"), "SettlementAsOf": date, "PaymentReference": "2026-003"},
		"MailEnabled": true, "NeighborEmail": "test@example.invalid", "ShareDayOptions": []int{7, 30}, "ShareDefaultDays": 7,
		"LastSend": models.BelegSend{SentAt: date, Channel: "manual"},
	}
}

func TestInvoiceRoleControlsPreserveExportsAndSettlement(t *testing.T) {
	t.Parallel()
	for _, role := range []string{models.RoleViewer, models.RoleEditor, models.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			data := invoicePolishData()
			data["User"] = models.User{Role: role}
			page := accountPolishContent(t, "invoice_document", data)
			for _, want := range []string{"277,20", "45,00", "322,20", "144,00", "178,20", "Unveränderter Steuerhinweis", "frozen-hash",
				`/neighbors/2/beleg.pdf?year=1&amp;invoice_id=3`, `/neighbors/2/settlement.csv?year=1&amp;invoice_id=3`, `/documents/3/ebinterface.xml`,
				`data-beleg-print`, `data-beleg-copy`, `data-beleg-image`, `data-beleg-share`, "Zuletzt versendet"} {
				if !strings.Contains(page, want) {
					t.Errorf("invoice lost existing content/export %q", want)
				}
			}
			for _, action := range []string{"Zahlung verbuchen", "Per E-Mail senden", "Link erzeugen", "Rechnung stornieren", "Gutschrift erstellen"} {
				if strings.Contains(page, action) != (role != models.RoleViewer) {
					t.Errorf("action %q does not respect role %q", action, role)
				}
			}
			if role == models.RoleViewer && strings.Contains(page, `method="post"`) {
				t.Fatal("viewer invoice still contains a mutation form")
			}
		})
	}
}

func TestInvoiceActionsAreGroupedByTask(t *testing.T) {
	t.Parallel()
	page := accountPolishContent(t, "invoice_document", invoicePolishData())
	primary := regexp.MustCompile(`(?s)<section class="document-actions__group" aria-labelledby="invoice-primary-actions">.*?</section>`).FindString(page)
	delivery := regexp.MustCompile(`(?s)<section class="document-actions__group" aria-labelledby="invoice-delivery-actions">.*?</section>`).FindString(page)
	exports := regexp.MustCompile(`(?s)<details class="document-actions__group disclosure">.*?</details>`).FindString(page)
	for _, label := range []string{"Rechnung als PDF", "Zahlung verbuchen"} {
		if !strings.Contains(primary, label) {
			t.Errorf("primary invoice task missing %q", label)
		}
	}
	for _, label := range []string{"Per E-Mail senden", "data-beleg-share", "beleg/mark-sent", "Zuletzt versendet"} {
		if !strings.Contains(delivery, label) {
			t.Errorf("delivery task missing %q", label)
		}
	}
	for _, label := range []string{"Weitere Formate und Export", "data-beleg-copy", "data-beleg-image", "data-beleg-print", "settlement.csv", "ebinterface.xml"} {
		if !strings.Contains(exports, label) {
			t.Errorf("secondary export disclosure missing %q", label)
		}
	}
	if !strings.Contains(page, `class="beleg__settlement-total">Offen: 178,20`) {
		t.Fatal("current invoice balance must remain the prominent settlement amount")
	}
}

func TestSharedInvoicePreservesPortalFeedback(t *testing.T) {
	t.Parallel()
	data := invoicePolishData()
	delete(data, "User")
	data["Shared"], data["PortalFeedbackEnabled"], data["PortalToken"] = true, true, "test-token"
	page := accountPolishContent(t, "invoice_document", data)
	for _, want := range []string{`/s/portal/test-token/invoice.pdf`, `action="/s/portal/test-token/feedback"`, "Rechnungsstand bestätigen", "Einwand übermitteln", "178,20"} {
		if !strings.Contains(page, want) {
			t.Errorf("shared invoice lost public capability %q", want)
		}
	}
	for _, forbidden := range []string{"Zahlung verbuchen", "Link erzeugen", "Rechnung stornieren", "/neighbors/2/beleg/email", "settlement.csv", "ebinterface.xml"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("shared invoice exposes internal capability %q", forbidden)
		}
	}
}
