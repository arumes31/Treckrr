package einvoice

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRenderWithPaymentStatus(t *testing.T) {
	for _, tc := range []struct{ name, remaining, prepaid string }{
		{"unpaid", "120", "0.00"}, {"partial", "20", "100.00"}, {"paid", "0", "120.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invoice := testInvoice()
			before, _ := json.Marshal(invoice)
			status := PaymentStatus{Remaining: decimal.RequireFromString(tc.remaining),
				DueOn: invoice.IssuedOn.AddDate(0, 0, 14), AsOf: time.Now()}
			data, err := RenderWithPaymentStatus(invoice, status)
			if err != nil {
				t.Fatal(err)
			}
			var got invoiceXML
			if err := xml.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.TotalGross.Value != "120.00" || got.Prepaid == nil || got.Prepaid.Value != tc.prepaid ||
				got.Payable.Value != status.Remaining.StringFixed(2) || got.PaymentConditions.DueDate != "2026-10-13" {
				t.Fatalf("incorrect payment projection: %s", data)
			}
			if status.Remaining.IsZero() && (got.PaymentMethod.Bank != nil || got.PaymentMethod.NoPayment == nil) {
				t.Fatal("settled invoice still instructs a bank transfer")
			}
			after, _ := json.Marshal(invoice)
			if string(before) != string(after) {
				t.Fatal("frozen invoice changed")
			}
			if !strings.Contains(got.Comment, "Zahlungsstand vom") {
				t.Fatal("payment snapshot is not dated")
			}
		})
	}
}

func TestRenderWithPaymentStatusRejectsInvalidAmounts(t *testing.T) {
	for _, amount := range []string{"-0.01", "120.01", "0.001"} {
		t.Run(amount, func(t *testing.T) {
			invoice := testInvoice()
			_, err := RenderWithPaymentStatus(invoice, PaymentStatus{
				Remaining: decimal.RequireFromString(amount), DueOn: invoice.IssuedOn, AsOf: time.Now(),
			})
			if err == nil {
				t.Fatal("invalid payable amount exported")
			}
		})
	}
}

func TestRenderWithPaymentStatusRejectsCanceledInvoice(t *testing.T) {
	invoice := testInvoice()
	invoice.Status = "canceled"
	data, err := RenderWithPaymentStatus(invoice, PaymentStatus{
		Remaining: invoice.Content.Gross, DueOn: invoice.IssuedOn.AddDate(0, 0, 14), AsOf: time.Now(),
	})
	if err == nil || len(data) != 0 {
		t.Fatal("canceled invoice produced a payment demand")
	}
}
