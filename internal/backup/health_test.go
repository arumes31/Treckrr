package backup

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestCheckSchedule(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, cron string
		last       time.Time
		failed     bool
		want       string
	}{
		{"daily current", "0 3 * * *", now.Add(-7 * time.Hour), false, "ok"},
		{"daily late", "0 3 * * *", now.Add(-31 * time.Hour), false, "stale"},
		{"weekly current", "0 3 * * 2", now.Add(-5 * 24 * time.Hour), false, "ok"},
		{"weekly late", "0 3 * * 0", now.Add(-9 * 24 * time.Hour), false, "stale"},
		{"hourly grace", "0 * * * *", now.Add(-time.Hour), false, "ok"},
		{"hourly late", "0 * * * *", now.Add(-2 * time.Hour), false, "stale"},
		{"future untrusted", "0 3 * * *", now.Add(time.Hour), false, "stale"},
		{"never", "0 3 * * *", time.Time{}, false, "never"},
		{"first failed", "0 3 * * *", time.Time{}, true, "failed"},
		{"disabled", "", time.Time{}, true, "disabled"},
		{"invalid", "broken", now, false, "invalid"},
		{"failed", "0 3 * * *", now, true, "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := CheckSchedule(tt.cron, tt.last, tt.failed, now)
			if h.State != tt.want {
				t.Fatalf("state=%s want %s (%+v)", h.State, tt.want, h)
			}
		})
	}
}

func TestCheckScheduleDSTAndDeadline(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	// JSON timestamps retain an offset, not an IANA zone. Scheduling must still
	// use the scheduler's current local zone over the spring DST transition.
	last := time.Date(2026, 3, 28, 3, 0, 0, 0, loc).UTC()
	now := time.Date(2026, 3, 29, 4, 0, 0, 0, loc)
	h := CheckSchedule("0 3 * * *", last, false, now)
	want := time.Date(2026, 3, 29, 3, 0, 0, 0, loc)
	if !h.Expected.Equal(want) || h.State != "ok" {
		t.Fatalf("DST health=%+v", h)
	}
	if got := CheckSchedule("0 3 * * *", last, false, h.Deadline); got.State != "ok" {
		t.Fatal(got)
	}
	if got := CheckSchedule("0 3 * * *", last, false, h.Deadline.Add(time.Nanosecond)); got.State != "stale" {
		t.Fatal(got)
	}
}
