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

// The three source-only views from the surface review need populated-state
// regressions as well as the shared browser layout/overflow checks.
func TestCarryForwardPreviewRetainsBalancesAndGuardValues(t *testing.T) {
	t.Parallel()
	for _, amount := range []string{"178.20", "-60.00", "0.00"} {
		for _, role := range []string{models.RoleViewer, models.RoleEditor} {
			t.Run(amount+"/"+role, func(t *testing.T) {
				t.Parallel()
				source := decimal.RequireFromString(amount)
				target := decimal.RequireFromString("13.45")
				page := mainContent(t, execPage(t, "carry_forward", map[string]any{
					"User": models.User{Role: role}, "Neighbor": models.Neighbor{ID: 2, Name: "Testhof"},
					"Year": models.BillingYear{ID: 1, Year: 2026}, "NextID": 3, "NextYear": 2027,
					"Back":    "/neighbors/2?year=1&view=payments",
					"Preview": map[string]any{"Source": source, "Target": target, "TargetAfter": target.Add(source)},
				}))
				for _, want := range []string{
					Money(source), Money(target), Money(target.Add(source)), "0,00 €",
					`href="/neighbors/2?year=1&amp;view=bookings"`, `href="/neighbors/2?year=3&amp;view=bookings"`,
					`role="region" aria-label="Salden vor und nach dem Übertrag" tabindex="0"`,
					"kein Zahlungseingang", "Es fließt kein Geld",
				} {
					if !strings.Contains(page, want) {
						t.Errorf("carry preview lost value/navigation %q", want)
					}
				}
				canSubmit := role == models.RoleEditor && !source.IsZero()
				for _, control := range []string{
					`action="/neighbors/2/carry-forward"`,
					`name="expected_source" value="` + source.StringFixed(2) + `"`,
					`name="expected_target" value="13.45"`,
				} {
					if strings.Contains(page, control) != canSubmit {
						t.Errorf("carry preview has wrong guard/control %q", control)
					}
				}
			})
		}
	}
}

// TestPaymentImportResultPreservesAmountsLinksAndCorrectionEligibility keeps
// amounts, statuses, escaped text and links, and offers a counter-entry only for
// booked rows and only to writers.
func TestPaymentImportResultPreservesAmountsLinksAndCorrectionEligibility(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, role := range []string{models.RoleViewer, models.RoleEditor} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			rows := []store.PaymentImportRow{
				{ID: 11, RowNo: 1, Status: store.PaymentImportBooked, PaymentID: 1, Amount: decimal.RequireFromString("100.25"), NeighborID: 2, YearID: 3, TransactionDate: &day, Reference: "Rechnung <2026-001>", PayerName: "Test & Hof"},
				{ID: 12, RowNo: 2, Status: store.PaymentImportReversed, PaymentID: 2, ReversalPaymentID: 4, Amount: decimal.RequireFromString("40.50"), NeighborID: 2, YearID: 3},
				{ID: 13, RowNo: 3, Status: store.PaymentImportUnmatched, Amount: decimal.RequireFromString("7.35"), Reason: "Keine eindeutige Zuordnung"},
				{ID: 14, RowNo: 4, Status: store.PaymentImportDuplicate, Amount: decimal.RequireFromString("8.10")},
			}
			page := mainContent(t, execPage(t, "payment_import_batch", map[string]any{
				"User": models.User{Role: role}, "Rows": rows,
				"Batch": store.PaymentImportBatch{ID: 5, Status: "completed", CreatedAt: day, SourceSHA256: "1234567890abcdef", TotalRows: 4, BookedRows: 2, UnmatchedRows: 1, DuplicateRows: 1},
			}))
			for _, want := range []string{
				"100,25 €", "40,50 €", "7,35 €", "8,10 €", "verbucht", "korrigiert", "nicht zugeordnet", "bereits importiert",
				"Rechnung &lt;2026-001&gt;", "Test &amp; Hof", "Keine eindeutige Zuordnung",
				`href="/payments/import/batches/5/report.csv"`, `href="/neighbors/2?year=3&amp;view=payments"`,
				`role="region" aria-label="Ergebnis des Zahlungsimports" tabindex="0"`,
			} {
				if !strings.Contains(page, want) {
					t.Errorf("payment import result lost %q", want)
				}
			}
			if strings.Contains(page, `action="/payments/import/rows/11/reverse"`) != (role == models.RoleEditor) {
				t.Error("booked payment correction does not match writer role")
			}
			for _, id := range []string{"12", "13", "14"} {
				if strings.Contains(page, `action="/payments/import/rows/`+id+`/reverse"`) {
					t.Errorf("non-correctable row %s offers a counter-entry", id)
				}
			}
		})
	}
}

// TestMailDeliveryDetailPreservesStatusSpecificSafetyControls offers only the
// action that fits each mail status, disables it for redacted mail, and keeps
// confirmations on destructive actions.
func TestMailDeliveryDetailPreservesStatusSpecificSafetyControls(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		status, action string
		redacted       bool
	}{
		{status: "pending"}, {status: "sending"}, {status: "sent"},
		{status: "failed", action: "retry"}, {status: "failed", action: "retry", redacted: true},
		{status: "ambiguous", action: "force-resend"}, {status: "ambiguous", action: "force-resend", redacted: true},
		{status: "held", action: "release"},
	} {
		name := tc.status
		if tc.redacted {
			name += "/redacted"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			item := store.MailOutboxItem{ID: 7, Status: tc.status, CreatedAt: day, Recipient: "test@example.invalid", Subject: "Rechnung <2026-001>", Redacted: tc.redacted, Attempts: 2}
			page := mainContent(t, execPage(t, "mail_outbox_detail", map[string]any{
				"User": models.User{Role: models.RoleAdmin, IsAdmin: true}, "MailEnabled": false,
				"Mail": store.MailOutboxDetail{Item: item, Attempts: []store.MailAttempt{{AttemptNo: 2, StartedAt: day, Outcome: "ambiguous", Detail: "SMTP <test>"}}},
			}))
			for _, want := range []string{item.StatusLabel(), "Rechnung &lt;2026-001&gt;", "test@example.invalid", "Versuch 2", "SMTP &lt;test&gt;", `href="/admin/mail"`} {
				if !strings.Contains(page, want) {
					t.Errorf("mail detail lost %q", want)
				}
			}
			for _, action := range []string{"retry", "force-resend", "release", "discard"} {
				form := regexp.MustCompile(`(?s)<form[^>]+action="/admin/mail/7/` + action + `"[^>]*>.*?</form>`).FindString(page)
				want := action == tc.action || (tc.status == "held" && action == "discard")
				if (form != "") != want {
					t.Errorf("mail state %s exposes wrong action %s", tc.status, action)
				}
				if want && strings.Contains(form, " disabled") != tc.redacted {
					t.Errorf("redacted=%v mail action %s has wrong disabled state", tc.redacted, action)
				}
				if want && (action == "force-resend" || action == "discard") && !strings.Contains(form, "data-confirm-danger") {
					t.Errorf("mail action %s lost destructive confirmation", action)
				}
			}
			if tc.status == "ambiguous" && !strings.Contains(page, "Duplikat erzeugen") {
				t.Error("ambiguous delivery lost duplicate-risk explanation")
			}
		})
	}
}

// TestPersonalAccountControlsRemainAvailableForEveryRole keeps 2FA, passkey,
// session and password self-service operable for every role.
func TestPersonalAccountControlsRemainAvailableForEveryRole(t *testing.T) {
	t.Parallel()
	for _, role := range []string{models.RoleViewer, models.RoleEditor, models.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			user := models.User{Username: "test-user", Role: role, IsAdmin: role == models.RoleAdmin, TotpEnabled: true}
			data := map[string]any{
				"User": user, "RecoveryRemaining": 2,
				"Passkeys": []models.WebauthnCredential{{ID: 7, Name: "Test passkey"}},
				"Sessions": []models.Session{{Token: "current-session", Current: true}, {Token: "other-session"}},
			}
			profile := mainContent(t, execPage(t, "profile", data))
			for _, action := range []string{"/account/2fa/recovery", "/account/2fa/disable", "/account/passkeys/7/delete", "/account/sessions/revoke", "/account/sessions/revoke-others", "/logout"} {
				if !strings.Contains(profile, `action="`+action+`"`) {
					t.Errorf("role %s lost personal control %s", role, action)
				}
			}
			if strings.Contains(profile, `name="token" value="current-session"`) || !strings.Contains(profile, `name="token" value="other-session"`) {
				t.Error("personal session revoke no longer distinguishes the current session")
			}
			for _, pageName := range []string{"account_password", "account_2fa"} {
				page := mainContent(t, execPage(t, pageName, data))
				if !strings.Contains(page, `method="post" action="/account/`) || !strings.Contains(page, `type="submit"`) || strings.Contains(page, "readonly-fields") {
					t.Errorf("role %s lost operable self-service page %s", role, pageName)
				}
			}
		})
	}
}
