package backup

import (
	"context"
	"strings"
	"time"
)

// ScheduleHealth describes one destination without changing its schedule.
type ScheduleHealth struct {
	State       string // ok, stale, failed, never, disabled, invalid
	LastSuccess time.Time
	Expected    time.Time
	Deadline    time.Time
}

// CheckSchedule evaluates the next run after the last success. The grace period
// is one tenth of the interval, bounded to 5 minutes..2 hours. Cron's timezone
// and DST behavior is shared with the scheduler rather than approximated in days.
func CheckSchedule(expr string, last time.Time, failed bool, now time.Time) ScheduleHealth {
	h := ScheduleHealth{LastSuccess: last}
	if strings.TrimSpace(expr) == "" {
		h.State = "disabled"
		return h
	}
	schedule, ok := parseCron(expr)
	if !ok {
		h.State = "invalid"
		return h
	}
	if last.IsZero() {
		h.State = "never"
		h.Expected = schedule.Next(now)
		if failed {
			h.State = "failed"
		}
		return h
	}
	h.Expected = schedule.Next(last.In(now.Location()))
	interval := h.Expected.Sub(last)
	grace := min(max(interval/10, 5*time.Minute), 2*time.Hour)
	h.Deadline = h.Expected.Add(grace)
	switch {
	case failed:
		h.State = "failed"
	case last.After(now), h.Expected.IsZero(), now.After(h.Deadline):
		h.State = "stale"
	default:
		h.State = "ok"
	}
	return h
}

// Schedule returns the same live destination settings used by the scheduler.
func (s *Service) Schedule(ctx context.Context) Settings { return s.currentSettings(ctx) }
