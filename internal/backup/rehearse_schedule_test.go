package backup

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduledRehearsalDisabledAndFailed(t *testing.T) {
	for _, tt := range []struct {
		name, url, cron string
		failed          bool
	}{
		{"disabled", "", "", false},
		{"manual only", "postgres://scratch.invalid/postgres", "", false},
		{"no target", "", "0 5 1 * *", false},
		{"missing dump", "postgres://scratch.invalid/postgres", "0 5 1 * *", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			s := New(Options{EncKey: "synthetic-test-key", Dir: dir, StatusFile: filepath.Join(dir, "status.json"), RehearseURL: tt.url, RehearseCron: tt.cron}, nil)
			s.runScheduledRehearsal(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now())
			status := s.readStatus()
			if tt.failed {
				if status.RehearsalOK == nil || *status.RehearsalOK || s.rehearsalRetryAt.IsZero() {
					t.Fatalf("failure not recorded: %+v", status)
				}
			} else if status.RehearsalOK != nil || !s.rehearsalRetryAt.IsZero() {
				t.Fatalf("disabled job ran: %+v", status)
			}
		})
	}
}
