//go:build integration

package backup

import (
	"context"
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
	t.Cleanup(func() { _ = pool.Close() })
	if err := appdb.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	var paymentID, neighborID, yearID, baseID int64
	if err := pool.QueryRowContext(t.Context(), `
		WITH base AS (
			INSERT INTO price_bases (year,name)
			SELECT COALESCE(max(year),3000)+1,'Synthetic rehearsal basis' FROM price_bases
			RETURNING id,year
		), year AS (
			INSERT INTO billing_years (year,base_id)
			SELECT GREATEST(base.year,(SELECT COALESCE(max(year),3000)+1 FROM billing_years)),base.id FROM base
			RETURNING id,base_id
		), neighbor AS (
			INSERT INTO neighbors (name)
			SELECT 'Synthetic rehearsal neighbor ' || id FROM year RETURNING id
		), payment AS (
			INSERT INTO payments (billing_year_id,neighbor_id,amount,note)
			SELECT year.id,neighbor.id,12.34,'Synthetic rehearsal payment' FROM year,neighbor
			RETURNING id,billing_year_id,neighbor_id
		)
		SELECT payment.id,payment.neighbor_id,payment.billing_year_id,year.base_id
		FROM payment JOIN year ON year.id=payment.billing_year_id`).Scan(&paymentID, &neighborID, &yearID, &baseID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, fixture := range []struct {
			table, query string
			id           int64
		}{
			{"payments", "DELETE FROM payments WHERE id=$1", paymentID},
			{"neighbors", "DELETE FROM neighbors WHERE id=$1", neighborID},
			{"billing_years", "DELETE FROM billing_years WHERE id=$1", yearID},
			{"price_bases", "DELETE FROM price_bases WHERE id=$1", baseID},
		} {
			if _, err := pool.ExecContext(ctx, fixture.query, fixture.id); err != nil {
				t.Errorf("cleanup %s fixture: %v", fixture.table, err)
			}
		}
	})
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
	// The archive has the old amount. Restoring over the source would revert
	// this later payment correction, even if all source row counts stayed equal.
	if _, err := pool.ExecContext(t.Context(), `UPDATE payments SET amount=98.76 WHERE id=$1`, paymentID); err != nil {
		t.Fatal(err)
	}
	generatedScratchNames := func() map[string]bool {
		t.Helper()
		// Match scratchName's timestamp/hex grammar and the older bare-hex
		// format, not operator databases that merely share the prefix.
		rows, err := pool.QueryContext(t.Context(), `SELECT datname FROM pg_database WHERE datname ~ '^treckrr_rehearsal_([0-9]+_)?[0-9a-f]{32}$'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names := make(map[string]bool)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names[name] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return names
	}
	existingScratch := generatedScratchNames()
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
	var paymentAmount string
	if err := pool.QueryRowContext(t.Context(), `SELECT amount::text FROM payments WHERE id=$1`, paymentID).Scan(&paymentAmount); err != nil {
		t.Fatal(err)
	}
	if paymentAmount != "98.7600" {
		t.Fatalf("rehearsal overwrote the source payment: amount=%s", paymentAmount)
	}
	for name := range generatedScratchNames() {
		if !existingScratch[name] {
			t.Errorf("rehearsal left a new scratch database behind: %s", name)
		}
	}
	// A fresh process observes the durable successful run and does not restore
	// again before the next configured monthly occurrence.
	New(opt, pool).runScheduledRehearsal(t.Context(), logger, time.Now().Add(time.Second))
	if after := s.readStatus(); !after.RestoreTested.Equal(st.RestoreTested) {
		t.Fatal("restart repeated a successful rehearsal before the next schedule")
	}
}
