package calc

import (
	"testing"

	"treckrr/internal/models"
)

func TestGespannRateAndCost(t *testing.T) {
	// Values taken directly from the source spreadsheet "Noppanschoftshilfe.xlsx".
	loads := map[string]models.LoadLevel{
		"leicht": {CostPerPS: 0.33},
		"mittel": {CostPerPS: 0.36},
		"schwer": {CostPerPS: 0.38},
	}
	machines := map[string]models.Machine{
		"Heckmähwerk":  {WorkingWidth: 2.4, CostPerAB: 10},
		"Frontmähwerk": {WorkingWidth: 3.06, CostPerAB: 12},
		"Schwader":     {WorkingWidth: 3.8, CostPerAB: 5},
		"Fräse":        {WorkingWidth: 2.0, CostPerAB: 18},
	}

	cases := []struct {
		name     string
		tractor  models.Tractor
		load     models.LoadLevel
		machines []models.Machine
		hours    float64
		want     float64
	}{
		{
			name:     "Mähen 4095 mittel + Heck + Front, 2.25h",
			tractor:  models.Tractor{PS: 100},
			load:     loads["mittel"],
			machines: []models.Machine{machines["Heckmähwerk"], machines["Frontmähwerk"]},
			hours:    2.25,
			want:     217.62,
		},
		{
			name:     "Schwadern 948 leicht + Schwader, 4h",
			tractor:  models.Tractor{PS: 50},
			load:     loads["leicht"],
			machines: []models.Machine{machines["Schwader"]},
			hours:    4,
			want:     142,
		},
		{
			name:     "Fräsen 9083 schwer + Fräse, 3h",
			tractor:  models.Tractor{PS: 94},
			load:     loads["schwer"],
			machines: []models.Machine{machines["Fräse"]},
			hours:    3,
			want:     215.16,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rate := GespannRate(tc.tractor, tc.load, tc.machines)
			got := Cost(tc.hours, rate)
			if !almostEqual(got, tc.want) {
				t.Fatalf("cost = %v, want %v (rate %v)", got, tc.want, rate)
			}
		})
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.005
}
