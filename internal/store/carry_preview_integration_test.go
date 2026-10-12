//go:build integration

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/d0linger/treckrr/internal/store"
)

func TestCarryPreviewRejectsChangedBalancesIntegration(t *testing.T) {
	for _, credit := range []bool{false, true} {
		name := "debt"
		if credit {
			name = "credit"
		}
		t.Run(name, func(t *testing.T) {
			ctx, _, st, source, target, nid, cleanup := raceSetup(t)
			defer cleanup()
			if credit {
				if _, err := st.AddNeighborLedger(ctx, source, nid, decimal.NewFromInt(-260), "Guthaben", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			before, err := st.ListNeighborLedger(ctx, source, nid)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := st.PreviewCarryForward(ctx, nid, source, target)
			if err != nil {
				t.Fatal(err)
			}
			afterRead, err := st.ListNeighborLedger(ctx, source, nid)
			if err != nil || len(afterRead) != len(before) {
				t.Fatalf("preview mutated source: %v", err)
			}
			if _, err := st.AddNeighborLedger(ctx, target, nid, decimal.NewFromInt(7), "New target posting", time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CarryForwardConfirmed(ctx, nid, source, target, time.Now(), "out", "in", &preview); !errors.Is(err, store.ErrCarryPreviewChanged) {
				t.Fatalf("changed target: got %v, want stale preview", err)
			}
			preview, err = st.PreviewCarryForward(ctx, nid, source, target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddNeighborLedger(ctx, source, nid, decimal.NewFromInt(3), "New source posting", time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CarryForwardConfirmed(ctx, nid, source, target, time.Now(), "out", "in", &preview); !errors.Is(err, store.ErrCarryPreviewChanged) {
				t.Fatalf("changed source: got %v, want stale preview", err)
			}
			preview, err = st.PreviewCarryForward(ctx, nid, source, target)
			if err != nil {
				t.Fatal(err)
			}
			moved, err := st.CarryForwardConfirmed(ctx, nid, source, target, time.Now(), "out", "in", &preview)
			if err != nil || !moved.Equal(preview.Source) {
				t.Fatalf("confirmed = %s, %v", moved, err)
			}
			links, err := st.LedgerTransferTargets(ctx, source, nid)
			if err != nil || len(links) != 1 {
				t.Fatalf("counterpart links = %v, %v", links, err)
			}
			for _, link := range links {
				if link.YearID != target || link.LedgerID <= 0 {
					t.Fatalf("wrong counterpart: %+v", link)
				}
			}
			fresh, err := st.PreviewCarryForward(ctx, nid, source, target)
			if err != nil || !fresh.Source.IsZero() || !fresh.Target.Equal(preview.TargetAfter()) {
				t.Fatalf("after = %+v, %v", fresh, err)
			}
			if _, err := st.CarryForwardConfirmed(ctx, nid, source, target, time.Now(), "out", "in", &preview); !errors.Is(err, store.ErrCarryPreviewChanged) {
				t.Fatalf("repeated confirmation = %v", err)
			}
			history, err := st.NeighborYearHistory(ctx, nid)
			if err != nil {
				t.Fatal(err)
			}
			var sum decimal.Decimal
			for _, row := range history {
				sum = sum.Add(row.Remaining)
			}
			if !sum.Equal(preview.TargetAfter()) {
				t.Fatalf("cross-year remainder %s duplicates carry; want %s", sum, preview.TargetAfter())
			}
		})
	}
}
