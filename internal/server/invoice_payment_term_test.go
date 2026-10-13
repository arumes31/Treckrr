package server

import (
	"testing"

	"github.com/d0linger/treckrr/internal/models"
)

func TestInvoicePaymentTerm(t *testing.T) {
	override := 30
	for _, tc := range []struct {
		name    string
		content models.InvoiceContent
		want    int
	}{
		{"legacy neighbor fallback", models.InvoiceContent{}, 30},
		{"frozen immediate", models.InvoiceContent{PaymentTermFrozen: true}, 0},
		{"frozen fourteen days", models.InvoiceContent{PaymentTermDays: 14, PaymentTermFrozen: true}, 14},
		{"known serialized term", models.InvoiceContent{PaymentTermDays: 7}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := invoicePaymentTerm(models.Invoice{Content: &tc.content},
				models.Company{PaymentTermDays: 14}, models.Neighbor{PaymentTermDays: &override})
			if got != tc.want {
				t.Fatalf("term=%d want=%d", got, tc.want)
			}
		})
	}
	if got := invoicePaymentTerm(models.Invoice{}, models.Company{PaymentTermDays: 14}, models.Neighbor{}); got != 14 {
		t.Fatalf("legacy company fallback=%d", got)
	}
}
