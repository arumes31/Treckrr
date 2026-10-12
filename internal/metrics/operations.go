package metrics

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// Operation is a closed set: no URL, identifier or personal data can become a label.
type Operation uint8

const (
	Dashboard Operation = iota
	NeighborAccount
	BookingSave
	PDF
	Export
	operationCount
)

var operationNames = [...]string{"dashboard", "neighbor_account", "booking_save", "pdf", "export"}
var operationHistograms [operationCount]operationHistogram

type operationHistogram struct {
	mu     sync.Mutex
	counts [9]int64
	sum    float64
}

// ObserveOperation records a bounded operation duration, rejecting invalid input.
func ObserveOperation(operation Operation, seconds float64) {
	if operation >= operationCount || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	h := &operationHistograms[operation]
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += seconds
	for i, bound := range histBuckets {
		if seconds <= bound {
			h.counts[i]++
			return
		}
	}
	h.counts[len(histBuckets)]++
}

// renderOperations emits exactly five series families, even for unvisited flows.
func renderOperations(b *strings.Builder) {
	const name = "treckrr_operation_duration_seconds"
	fmt.Fprintf(b, "# HELP %s Core operation HTTP duration.\n# TYPE %s histogram\n", name, name)
	for operation := range operationCount {
		h := &operationHistograms[operation]
		h.mu.Lock()
		counts, sum := h.counts, h.sum
		h.mu.Unlock()
		var cumulative int64
		for i, bound := range histBuckets {
			cumulative += counts[i]
			fmt.Fprintf(b, "%s_bucket{operation=%q,le=\"%g\"} %d\n", name, operationNames[operation], bound, cumulative)
		}
		cumulative += counts[len(histBuckets)]
		fmt.Fprintf(b, "%s_bucket{operation=%q,le=\"+Inf\"} %d\n", name, operationNames[operation], cumulative)
		fmt.Fprintf(b, "%s_sum{operation=%q} %g\n%s_count{operation=%q} %d\n", name, operationNames[operation], sum, name, operationNames[operation], cumulative)
	}
}
