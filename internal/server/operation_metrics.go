package server

import "github.com/d0linger/treckrr/internal/metrics"

// observeCoreOperation accepts only ServeMux's static route patterns, never raw
// request paths. Unknown routes intentionally create no metric series.
func observeCoreOperation(pattern string, seconds float64) {
	var operation metrics.Operation
	switch pattern {
	case "GET /{$}":
		operation = metrics.Dashboard
	case "GET /neighbors/{id}", "GET /neighbors/{id}/overview":
		operation = metrics.NeighborAccount
	case "POST /entries", "POST /entries/quick", "POST /neighbors/{id}/ledger":
		operation = metrics.BookingSave
	case "GET /neighbors/{id}/beleg.pdf", "GET /neighbors/{id}/overview.pdf", "GET /neighbors/{id}/mahnung.pdf":
		operation = metrics.PDF
	case "GET /export/year/{id}", "GET /export/neighbor/{id}":
		operation = metrics.Export
	default:
		return
	}
	metrics.ObserveOperation(operation, seconds)
}
