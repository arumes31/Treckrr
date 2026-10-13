//go:build integration

package store_test

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/models"
	"github.com/d0linger/treckrr/internal/store"
)

// TestBillingCompatibilityIntegration deliberately uses the public API shared
// by the single-invoice and multiple-invoice implementations. The same file can
// run against both revisions; BILLING_PARITY records compare actual results,
// not reimplemented business logic or a new-runtime-only expected snapshot.
// Every case owns a scratch database. No production data or golden files are used.
func TestBillingCompatibilityIntegration(t *testing.T) {
	cases := []struct {
		name, mode, vat, payment, ledger, credit, want string
		action                                         string
		unissued, incoming, fractional, voided         bool
	}{
		{name: "small_business_unissued", mode: "kleinunternehmer", want: "100", unissued: true},
		{name: "small_business_unpaid", mode: "kleinunternehmer", want: "100"},
		{name: "flat_rate_13_unpaid", mode: "pauschal", vat: "13", want: "113"},
		{name: "standard_20_unpaid", mode: "regel", vat: "20", want: "120"},
		{name: "standard_20_partial", mode: "regel", vat: "20", payment: "30", want: "90"},
		{name: "standard_20_fully_paid", mode: "regel", vat: "20", payment: "120", want: "0"},
		{name: "standard_20_overpaid_and_payout", mode: "regel", vat: "20", payment: "150", want: "-30", action: "payout"},
		{name: "manual_credit", mode: "kleinunternehmer", ledger: "-25", want: "75"},
		{name: "manual_debit", mode: "kleinunternehmer", ledger: "20", want: "120"},
		{name: "debit_beyond_paid_document", mode: "kleinunternehmer", ledger: "20", payment: "110", want: "10"},
		{name: "incoming_service", mode: "regel", vat: "20", incoming: true, want: "85"},
		{name: "credit_and_partial_payment", mode: "regel", vat: "20", credit: "24", payment: "30", want: "66"},
		{name: "storno_and_reissue_changed_tax", mode: "regel", vat: "20", credit: "12", payment: "30", want: "78", action: "storno"},
		{name: "skonto_two_percent", mode: "regel", vat: "20", payment: "117.60", want: "0", action: "skonto"},
		{name: "deleted_payment_and_restore", mode: "regel", vat: "20", payment: "40", want: "80", action: "delete_payment"},
		{name: "closed_year_settle_remaining", mode: "regel", vat: "20", payment: "30", want: "90", action: "settle"},
		{name: "carry_debit", mode: "regel", vat: "20", payment: "30", want: "90", action: "carry"},
		{name: "carry_credit", mode: "regel", vat: "20", payment: "150", want: "-30", action: "carry"},
		{name: "legacy_subcent_booking", mode: "kleinunternehmer", want: "10", unissued: true, fractional: true},
		{name: "voided_booking_before_issue", mode: "kleinunternehmer", want: "100", voided: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			st, _, yearID, neighborID, entryID := invoiceFixtureYear(t, false, 2025)
			date := time.Date(2025, 9, 10, 0, 0, 0, 0, time.UTC)
			company, err := st.GetCompany(ctx)
			billingParityOK(t, err)
			company.TaxMode, company.VATRate = tc.mode, billingParityDecimal(tc.vat)
			company.TaxID, company.TaxNote = "ATU12345678", "Synthetic billing compatibility notice"
			if tc.action == "skonto" {
				company.SkontoPct, company.SkontoDays = decimal.NewFromInt(2), 14
			}
			billingParityOK(t, st.UpdateCompany(ctx, company))
			if tc.fractional {
				entry, getErr := st.GetEntry(ctx, entryID)
				billingParityOK(t, getErr)
				entry.Cost, entry.UnitPrice = billingParityDecimal("9.995"), billingParityDecimal("9.995")
				billingParityOK(t, st.UpdateEntry(ctx, entry, nil))
			}
			if tc.voided {
				voidID, createErr := st.CreateEntry(ctx, &models.Entry{
					NeighborID: neighborID, BillingYearID: yearID, Date: date,
					TaskLabel: "Canceled synthetic work", Unit: "Ballen", Quantity: decimal.NewFromInt(2),
					UnitPrice: decimal.NewFromInt(25), Cost: decimal.NewFromInt(50),
				}, nil)
				billingParityOK(t, createErr)
				billingParityOK(t, st.SetEntryVoided(ctx, voidID, true, "Synthetic cancellation"))
			}
			if tc.ledger != "" {
				_, err = st.AddNeighborLedger(ctx, yearID, neighborID, billingParityDecimal(tc.ledger), "Synthetic manual posting", date)
				billingParityOK(t, err)
			}
			if tc.incoming {
				_, err = st.CreateLedgerBooking(ctx, store.LedgerBookingInput{
					YearID: yearID, NeighborID: neighborID, Date: date, Incoming: true,
					Booking: models.LedgerBooking{Version: 1, Kind: "quantity", TaskLabel: "Synthetic incoming work",
						Unit: "Ballen", Quantity: decimal.NewFromInt(5), UnitPrice: decimal.NewFromInt(7)},
				})
				billingParityOK(t, err)
			}
			var original models.Invoice
			if !tc.unissued {
				original, err = st.IssueInvoice(ctx, yearID, neighborID, 2025, date)
				billingParityOK(t, err)
				original, err = st.GetInvoiceDocument(ctx, original.ID)
				billingParityOK(t, err)
				if original.Content == nil {
					t.Fatal("issued document has no frozen content")
				}
				billingParityMoney(t, "invoice net excludes incoming/voided work", original.Content.Net, "100")
				billingParityMoney(t, "invoice VAT", original.Content.VATAmount, tc.vat)
				wantGross := decimal.NewFromInt(100).Add(billingParityDecimal(tc.vat))
				billingParityMoney(t, "invoice gross", original.Content.Gross, wantGross.String())
				if len(original.Content.Lines) != 1 {
					t.Fatalf("invoice lines = %d, want only the one own non-voided booking", len(original.Content.Lines))
				}
			}
			if tc.credit != "" {
				_, err = st.GutschriftInvoice(ctx, yearID, neighborID, billingParityDecimal(tc.credit), "Synthetic credit")
				billingParityOK(t, err)
			}
			if tc.payment != "" {
				input := store.PaymentInput{YearID: yearID, NeighborID: neighborID, Amount: billingParityDecimal(tc.payment),
					PaidOn: date, Note: "Synthetic payment", Method: "bank"}
				if tc.action == "skonto" {
					input.SkontoPct = decimal.NewFromInt(2)
				}
				_, err = st.RecordPayment(ctx, input)
				billingParityOK(t, err)
			}
			billingParitySnapshot(t, st, tc.name, "initial", yearID, neighborID, tc.want)

			switch tc.action {
			case "payout":
				amount, payoutErr := st.PayoutCredit(ctx, yearID, neighborID, date, "Synthetic payout")
				billingParityOK(t, payoutErr)
				billingParityMoney(t, "payout", amount, "30")
				billingParitySnapshot(t, st, tc.name, "after_payout", yearID, neighborID, "0")
			case "delete_payment":
				payments, listErr := st.ListPayments(ctx, yearID, neighborID)
				billingParityOK(t, listErr)
				if len(payments) != 1 {
					t.Fatalf("payments = %d, want 1", len(payments))
				}
				changed, deleteErr := st.DeletePayment(ctx, payments[0].ID)
				billingParityOK(t, deleteErr)
				if !changed {
					t.Fatal("payment was not deleted")
				}
				billingParitySnapshot(t, st, tc.name, "after_delete", yearID, neighborID, "120")
				changed, err = st.RestorePayment(ctx, payments[0].ID)
				billingParityOK(t, err)
				if !changed {
					t.Fatal("payment was not restored")
				}
				billingParitySnapshot(t, st, tc.name, "after_restore", yearID, neighborID, "80")
			case "settle":
				billingParityOK(t, st.SetYearStatus(ctx, yearID, "completed"))
				amount, settleErr := st.SettleRemaining(ctx, yearID, neighborID, date, "Synthetic settlement")
				billingParityOK(t, settleErr)
				billingParityMoney(t, "closed-year settlement", amount, "90")
				amount, settleErr = st.SettleRemaining(ctx, yearID, neighborID, date, "Synthetic settlement")
				billingParityOK(t, settleErr)
				billingParityMoney(t, "second settlement is a no-op", amount, "0")
				billingParitySnapshot(t, st, tc.name, "after_close_and_settle", yearID, neighborID, "0")
			case "carry":
				billingParityOK(t, st.SetYearStatus(ctx, yearID, "completed"))
				baseID, baseErr := st.CreateEmptyBase(ctx, 2026, "Synthetic carry target")
				billingParityOK(t, baseErr)
				targetID, yearErr := st.CreateBillingYear(ctx, 2026, baseID, "Synthetic carry target")
				billingParityOK(t, yearErr)
				billingParityOK(t, st.AddNeighborToYear(ctx, targetID, neighborID))
				amount, carryErr := st.CarryForwardRemaining(ctx, neighborID, yearID, targetID,
					time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "Synthetic carry out", "Synthetic carry in")
				billingParityOK(t, carryErr)
				billingParityMoney(t, "carry amount preserves sign", amount, tc.want)
				billingParitySnapshot(t, st, tc.name, "source_after_carry", yearID, neighborID, "0")
				billingParitySnapshot(t, st, tc.name, "target_after_carry", targetID, neighborID, tc.want)
			case "storno":
				reversal, stornoErr := st.StornoInvoice(ctx, yearID, neighborID, "Synthetic reversal")
				billingParityOK(t, stornoErr)
				billingParitySnapshot(t, st, tc.name, "after_storno", yearID, neighborID, "70")
				company.TaxMode, company.VATRate = "pauschal", decimal.NewFromInt(13)
				billingParityOK(t, st.UpdateCompany(ctx, company))
				// Corrections use today's date; the replacement must not precede them.
				_, err = st.IssueInvoice(ctx, yearID, neighborID, 2025, reversal.IssuedOn)
				billingParityOK(t, err)
				billingParitySnapshot(t, st, tc.name, "after_reissue", yearID, neighborID, "83")
			}
			if original.ID != 0 {
				stored, getErr := st.GetInvoiceDocument(ctx, original.ID)
				billingParityOK(t, getErr)
				if billingParityJSON(t, stored.Content) != billingParityJSON(t, original.Content) || stored.Content.Hash != original.Content.Hash {
					t.Fatal("settlement/correction changed original frozen invoice content or hash")
				}
			}
		})
	}
}

// Explicit projections remove only generated identities/timestamps. Raw decimal
// strings retain sub-cent information; document-vs-account dunning/closing
// differences remain visible for the differential caller to classify.
func billingParitySnapshot(t *testing.T, st *store.Store, scenario, checkpoint string, yearID, neighborID int64, want string) {
	t.Helper()
	ctx := t.Context()
	remaining, err := st.AccountRemaining(ctx, yearID, neighborID)
	billingParityOK(t, err)
	billingParityMoney(t, "account remaining", remaining, want)
	invoiceRemaining, err := st.InvoiceRemaining(ctx, yearID, neighborID)
	billingParityOK(t, err)
	year, err := st.GetBillingYear(ctx, yearID)
	billingParityOK(t, err)
	paid, open, credit, err := st.YearPaymentTotals(ctx, yearID)
	billingParityOK(t, err)
	out := map[string]any{
		"scenario": scenario, "checkpoint": checkpoint, "year": year.Year, "status": year.Status,
		"remaining": remaining.String(), "invoice_remaining": invoiceRemaining.String(),
		"year_totals": map[string]string{"paid": paid.String(), "open": open.String(), "credit": credit.String()},
	}
	entries, err := st.ListEntries(ctx, neighborID, yearID)
	billingParityOK(t, err)
	bookings := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		bookings = append(bookings, map[string]any{"date": entry.Date.Format(time.DateOnly), "task": entry.TaskLabel,
			"unit": entry.Unit, "quantity": entry.Quantity.String(), "unit_price": entry.UnitPrice.String(),
			"hours": entry.Hours.String(), "hourly_rate": entry.HourlyRate.String(), "cost": entry.Cost.String(),
			"fuel_label": entry.FuelAdjustmentLabel, "fuel_per_h": entry.FuelAdjustmentPerH.String(),
			"voided": entry.Voided, "void_reason": entry.VoidReason})
	}
	out["bookings"] = bookings
	ledger, err := st.ListNeighborLedger(ctx, yearID, neighborID)
	billingParityOK(t, err)
	postings := make([]map[string]any, 0, len(ledger))
	for _, entry := range ledger {
		postings = append(postings, map[string]any{"date": entry.Date.Format(time.DateOnly), "amount": entry.Amount.String(),
			"description": entry.Description, "voided": entry.Voided, "has_transfer": entry.TransferID != "", "booking": entry.Booking})
	}
	out["ledger"] = postings
	payments, err := st.ListPayments(ctx, yearID, neighborID)
	billingParityOK(t, err)
	cash := make([]map[string]any, 0, len(payments))
	for _, payment := range payments {
		cash = append(cash, map[string]any{"date": payment.PaidOn.Format(time.DateOnly), "amount": payment.Amount.String(),
			"method": payment.Method, "note": payment.Note, "invoice_number": payment.InvoiceNumber, "reversal": payment.Reversal})
	}
	out["payments"] = cash
	documents, err := st.ListInvoiceDocuments(ctx, yearID, neighborID)
	billingParityOK(t, err)
	sort.Slice(documents, func(i, j int) bool { return documents[i].Number < documents[j].Number })
	numbers := make(map[int64]string, len(documents))
	for _, document := range documents {
		numbers[document.ID] = document.Number
	}
	docs := make([]map[string]any, 0, len(documents))
	for _, document := range documents {
		if document.Content == nil {
			t.Fatal("stored document has no content")
		}
		reference := ""
		if document.ReferencesInvoiceID != nil {
			reference = numbers[*document.ReferencesInvoiceID]
		}
		docs = append(docs, map[string]any{"number": document.Number, "kind": document.Kind, "status": document.Status,
			"reference": reference, "payment_reference": document.PaymentReference,
			"content": document.Content, "hash": document.Content.Hash})
	}
	out["documents"] = docs
	journal, err := st.ListInvoiceJournal(ctx, yearID)
	billingParityOK(t, err)
	sort.Slice(journal, func(i, j int) bool { return journal[i].Number < journal[j].Number })
	journalRows := make([]map[string]any, 0, len(journal))
	for _, row := range journal {
		journalRows = append(journalRows, map[string]any{"number": row.Number, "kind": row.Kind, "status": row.Status,
			"net": row.Net.String(), "vat_rate": row.VATRate.String(), "vat_amount": row.VATAmount.String(),
			"gross": row.Gross.String(), "counts_for_revenue": row.CountsForRevenue()})
	}
	out["journal"] = journalRows
	dunning, err := st.DunningRows(ctx, yearID, 14, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	billingParityOK(t, err)
	dunningRows := make([]map[string]any, 0, len(dunning))
	for _, row := range dunning {
		dunningRows = append(dunningRows, map[string]any{"number": row.InvoiceNo, "due": row.DueOn.Format(time.DateOnly), "open": row.Open.String()})
	}
	out["dunning"] = dunningRows
	checks, err := st.YearClosingChecks(ctx, yearID)
	billingParityOK(t, err)
	closing := make(map[string]any, len(checks))
	for _, check := range checks {
		closing[check.Key] = map[string]any{"count": check.Count, "amount": check.Amount.String()}
	}
	out["closing"] = closing
	summaries, err := st.YearNeighborSummaries(ctx, yearID)
	billingParityOK(t, err)
	if len(summaries) != 1 {
		t.Fatalf("neighbor summaries = %d, want 1", len(summaries))
	}
	summary := summaries[0]
	billingParityMoney(t, "dashboard remaining", summary.Remaining, want)
	out["summary"] = map[string]any{"cost": summary.Cost.String(), "payable": summary.Payable.String(),
		"paid_amount": summary.PaidAmount.String(), "remaining": summary.Remaining.String(), "hours": summary.Hours.String(),
		"entries": summary.Entries, "paid": summary.Paid, "credit": summary.Credit}
	history, err := st.NeighborYearHistory(ctx, neighborID)
	billingParityOK(t, err)
	historyRows := make([]map[string]any, 0, len(history))
	for _, row := range history {
		if row.YearID == yearID {
			billingParityMoney(t, "cross-year history remaining", row.Remaining, want)
		}
		historyRows = append(historyRows, map[string]any{"year": row.Year, "status": row.Status, "cost": row.Cost.String(),
			"ledger": row.Ledger.String(), "net": row.Net.String(), "payable": row.Payable.String(), "paid_amount": row.PaidAmount.String(),
			"remaining": row.Remaining.String(), "paid": row.Paid, "credit": row.Credit})
	}
	out["history"] = historyRows
	t.Logf("BILLING_PARITY %s", billingParityJSON(t, out))
}

func billingParityOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func billingParityDecimal(value string) decimal.Decimal {
	if value == "" {
		return decimal.Zero
	}
	return decimal.RequireFromString(value)
}

func billingParityMoney(t *testing.T, label string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(billingParityDecimal(want)) {
		t.Fatalf("%s = %s, want %s", label, got, want)
	}
}

func billingParityJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	billingParityOK(t, err)
	return string(body)
}
