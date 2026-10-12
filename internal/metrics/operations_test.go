package metrics

import (
	"math"
	"strings"
	"testing"
)

func TestOperationMetricsFixedCardinality(t *testing.T) {
	var before strings.Builder
	renderOperations(&before)
	for _, input := range []float64{-1, math.NaN(), math.Inf(1)} {
		ObserveOperation(Dashboard, input)
	}
	ObserveOperation(Operation(255), 1)
	var after strings.Builder
	renderOperations(&after)
	if before.String() != after.String() {
		t.Fatal("invalid observations changed metrics")
	}
	ObserveOperation(BookingSave, .25)
	after.Reset()
	renderOperations(&after)
	if strings.Count(after.String(), "_count{operation=") != 5 {
		t.Fatal("unexpected operation cardinality")
	}
	if !strings.Contains(after.String(), `operation="booking_save"`) || strings.Contains(after.String(), "NaN") {
		t.Fatal(after.String())
	}
}

func BenchmarkObserveOperation(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ObserveOperation(BookingSave, .01)
	}
}
