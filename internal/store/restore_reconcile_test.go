package store_test

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/d0linger/treckrr/internal/db"
)

// TestResetPoolKeepsPoolUsable proves the *sql.DB equivalent of pgxpool.Reset():
// after discarding the pooled connections the pool still serves queries (it
// reopens fresh connections on demand).
func TestResetPoolKeepsPoolUsable(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var n int
	if err := pool.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("pre-reset query: n=%d err=%v", n, err)
	}
	db.ResetPool(pool)
	if err := pool.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("post-reset query must still work: n=%d err=%v", n, err)
	}
}

// TestReconcileAfterRestoreReappliesMigration mirrors the real hazard: restoring a
// backup that predates migration 0024 leaves the schema a version behind (its
// columns and schema_migrations row are the backup's), so every query touching the
// new columns fails. The post-restore reconcile must bring it forward in one shot,
// so no container restart is needed — the fix ported from Parkrr.
//
// It runs against its OWN throwaway database, not the shared one. The previous
// version dropped 0024's seventeen columns on the SHARED invoices table and let
// the reconcile re-add them — but Postgres counts dropped columns against the
// 1600-column table limit FOREVER, so every suite run burned seventeen slots
// until, after enough runs, migration 0024 could not apply at all and the whole
// suite collapsed (SQLSTATE 54011). It also ran DELETE FROM invoices mid-suite,
// silently eating other tests' fixtures. A scratch database resets both problems
// on every run and needs no cleanup discipline at all.
func TestReconcileAfterRestoreReappliesMigration(t *testing.T) {
	ctx := context.Background()
	st, pool := scratchUnmigratedStore(t)

	// Apply the actual historical migrations to this otherwise empty owned DB.
	// Dropping only 0024's columns from a current schema leaves newer migration
	// markers behind, so 0081 would never remove the recreated single-invoice index.
	migrations := os.DirFS("../db/migrations")
	names, err := fs.ReadDir(migrations, ".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, `CREATE TABLE schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.IsDir() || !strings.HasSuffix(name.Name(), ".sql") || name.Name() >= "0024_" {
			continue
		}
		content, err := fs.ReadFile(migrations, name.Name())
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("historical migration %s: %v", name.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, name.Name()); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// Precondition: a query using a 0024 column now fails — the "stale schema" state.
	if _, err := pool.ExecContext(ctx, `SELECT net FROM invoices LIMIT 1`); err == nil {
		t.Fatalf("precondition: invoices.net should be gone after the simulated old restore")
	}

	// The reconcile that runs at the end of a restore must fix it without a restart.
	if err := st.ReconcileAfterRestore(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 0024 is recorded again, and the previously-failing queries run.
	var has bool
	if err := pool.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name='0024_invoice_snapshot.sql')`).Scan(&has); err != nil || !has {
		t.Errorf("migration 0024 must be re-applied after restore (has=%v err=%v)", has, err)
	}
	if _, err := pool.ExecContext(ctx, `SELECT net, kind, status, payment_reference FROM invoices LIMIT 1`); err != nil {
		t.Errorf("invoice snapshot columns must exist after reconcile: %v", err)
	}
	if _, err := pool.ExecContext(ctx, `SELECT iban FROM company LIMIT 1`); err != nil {
		t.Errorf("company.iban must exist after reconcile: %v", err)
	}
	if err := pool.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name='0081_invoice_sources.sql')`).Scan(&has); err != nil || !has {
		t.Errorf("migration 0081 must be applied after restore (has=%v err=%v)", has, err)
	}
	if err := pool.QueryRowContext(ctx, `SELECT to_regclass('invoices_active_per_neighbor_year') IS NULL
		AND to_regclass('invoices_active_account') IS NOT NULL
		AND to_regclass('invoice_entries_active_source') IS NOT NULL`).Scan(&has); err != nil || !has {
		t.Errorf("restored schema must support multiple invoices with unique source claims (has=%v err=%v)", has, err)
	}
}
