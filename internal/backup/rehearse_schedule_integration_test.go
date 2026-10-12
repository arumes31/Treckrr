//go:build integration

package backup

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/d0linger/treckrr/internal/db"
)

func TestRehearsalSchedulePersistsRetryAcrossRestart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := appdb.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := appdb.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	// This test owns the rehearsal clocks in the disposable test database.
	if _, err := pool.ExecContext(t.Context(), `UPDATE backup_scheduler_state SET rehearsal_last_success=NULL,rehearsal_attempt_at=NULL,rehearsal_retry_at=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opt := Options{EncKey: "synthetic-test-key", Dir: dir, StatusFile: filepath.Join(dir, "status.json"), RehearseURL: dsn, RehearseCron: "0 5 1 * *"}
	s := New(opt, pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now().Truncate(time.Microsecond)
	s.runScheduledRehearsal(t.Context(), logger, now)
	var last, attempt sql.NullTime
	var retry time.Time
	if err := pool.QueryRowContext(t.Context(), `SELECT rehearsal_last_success,rehearsal_attempt_at,rehearsal_retry_at FROM backup_scheduler_state WHERE id=1`).Scan(&last, &attempt, &retry); err != nil {
		t.Fatal(err)
	}
	if last.Valid || attempt.Valid || !retry.After(now) {
		t.Fatalf("failure clocks: last=%v attempt=%v retry=%v", last, attempt, retry)
	}
	next := New(opt, pool)
	next.runScheduledRehearsal(t.Context(), logger, now.Add(time.Minute))
	var unchanged time.Time
	if err := pool.QueryRowContext(t.Context(), `SELECT rehearsal_retry_at FROM backup_scheduler_state WHERE id=1`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !retry.Equal(unchanged) {
		t.Fatal("restart ignored durable retry clock")
	}
	// A crashed run is never displayed as a successful rehearsal.
	if err := next.updateStatus(func(st *Status) { ok := true; st.RehearsalOK = &ok }); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(t.Context(), `UPDATE backup_scheduler_state SET rehearsal_attempt_at=$1 WHERE id=1`, now); err != nil {
		t.Fatal(err)
	}
	next.runScheduledRehearsal(t.Context(), logger, now.Add(2*time.Minute))
	st := next.readStatus()
	if st.RehearsalOK == nil || *st.RehearsalOK {
		t.Fatal("interrupted rehearsal remained healthy")
	}
}

// TestRehearsalScheduleRestoresAndPersistsSuccessIntegration exercises the
// scheduled path with real encrypted pg_dump/pg_restore, not a mocked success.
func TestRehearsalScheduleRestoresAndPersistsSuccessIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	pool, err := appdb.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := appdb.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(t.Context(), `UPDATE backup_scheduler_state SET rehearsal_last_success=NULL,rehearsal_attempt_at=NULL,rehearsal_retry_at=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	countSourceRows := func() int64 {
		t.Helper()
		var count int64
		if err := pool.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM entries)+(SELECT count(*) FROM neighbor_ledger)+(SELECT count(*) FROM payments)+(SELECT count(*) FROM invoices)`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := countSourceRows()
	dir := t.TempDir()
	opt := Options{DatabaseURL: dsn, EncKey: "synthetic-scheduled-rehearsal-key", Dir: dir, StatusFile: filepath.Join(dir, "status.json"), RehearseURL: dsn, RehearseCron: "0 5 1 * *", SkipStartupCleanup: true}
	s := New(opt, pool)
	// Prepare an encrypted real archive without exercising volume retention:
	// this test's contract is the scheduler/restore clock, not local pruning.
	dbURL, env, err := dbURLEnv(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "pg_dump", "--format=custom", "--no-owner", "--no-privileges", dbURL) // #nosec G204 G702 -- fixed executable and separate argv; disposable TEST_DATABASE_URL, password passed via environment
	cmd.Env = env
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("prepare fixture dump: %v", err)
	}
	enc, err := encrypt(raw, s.secret)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("treckrr-schedule-fixture.dump.enc", enc, 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.runScheduledRehearsal(t.Context(), logger, time.Now())
	var last, attempt, retry sql.NullTime
	if err := pool.QueryRowContext(t.Context(), `SELECT rehearsal_last_success,rehearsal_attempt_at,rehearsal_retry_at FROM backup_scheduler_state WHERE id=1`).Scan(&last, &attempt, &retry); err != nil {
		t.Fatal(err)
	}
	st := s.readStatus()
	if !last.Valid || attempt.Valid || retry.Valid || st.RehearsalOK == nil || !*st.RehearsalOK || st.RestoreTested.IsZero() {
		t.Fatalf("scheduled success not persisted: last=%v attempt=%v retry=%v status=%+v", last, attempt, retry, st)
	}
	if after := countSourceRows(); after != before {
		t.Fatalf("source billing row count changed: before=%d after=%d", before, after)
	}
	// A fresh process observes the durable successful run and does not restore
	// again before the next configured monthly occurrence.
	New(opt, pool).runScheduledRehearsal(t.Context(), logger, time.Now().Add(time.Second))
	if after := s.readStatus(); !after.RestoreTested.Equal(st.RestoreTested) {
		t.Fatal("restart repeated a successful rehearsal before the next schedule")
	}
}
