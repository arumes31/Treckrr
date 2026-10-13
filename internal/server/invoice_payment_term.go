package server

import "github.com/d0linger/treckrr/internal/models"

// invoicePaymentTerm uses frozen terms, including an explicit zero. Only
// historical documents with missing terms retain the established live fallback.
func invoicePaymentTerm(invoice models.Invoice, company models.Company, neighbor models.Neighbor) int {
	if c := invoice.Content; c != nil && (c.PaymentTermFrozen || c.PaymentTermDays > 0) {
		return c.PaymentTermDays
	}
	if neighbor.PaymentTermDays != nil {
		return *neighbor.PaymentTermDays
	}
	return company.EffectiveTermDays()
}
