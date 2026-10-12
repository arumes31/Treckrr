package store_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/store"
)

func TestCarryPreviewTargetAfter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, target, want string }{
		{"debt", "100", "20", "120"},
		{"credit", "-60", "20", "-40"},
		{"offset", "100", "-100", "0"},
		{"zero", "0", "20", "20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := store.CarryPreview{Source: decimal.RequireFromString(tc.source), Target: decimal.RequireFromString(tc.target)}
			if !p.TargetAfter().Equal(decimal.RequireFromString(tc.want)) {
				t.Fatalf("target after = %s, want %s", p.TargetAfter(), tc.want)
			}
		})
	}
}
