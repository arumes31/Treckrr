// Package calc implements the cost model derived from the source spreadsheet.
//
//	tractor hourly rate = PS * cost_per_PS(load level)
//	machine hourly rate = working width * cost_per_AB
//	gespann hourly rate = tractor rate + sum(machine rates)
//	entry cost          = hours * gespann hourly rate
package calc

import (
	"math"

	"treckrr/internal/models"
)

// TractorRate returns the hourly rate for a tractor at a given load level.
func TractorRate(t models.Tractor, l models.LoadLevel) float64 {
	return round2(t.PS * l.CostPerPS)
}

// MachineRate returns the hourly rate contribution of a machine.
func MachineRate(m models.Machine) float64 {
	return round2(m.WorkingWidth * m.CostPerAB)
}

// GespannRate sums the tractor rate and all machine rates.
func GespannRate(t models.Tractor, l models.LoadLevel, machines []models.Machine) float64 {
	rate := TractorRate(t, l)
	for _, m := range machines {
		rate += MachineRate(m)
	}
	return round2(rate)
}

// Cost multiplies hours by the hourly rate.
func Cost(hours, hourlyRate float64) float64 {
	return round2(hours * hourlyRate)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
