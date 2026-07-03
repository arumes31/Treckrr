package models

import (
	"math"
	"strconv"
)

// round2 rounds to two decimal places (currency).
func round2(f float64) float64 { return math.Round(f*100) / 100 }

// formatFloat renders a float without trailing zeros, e.g. 100 -> "100",
// 2.25 -> "2.25".
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
