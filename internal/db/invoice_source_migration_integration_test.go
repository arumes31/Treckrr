//go:build integration

package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// This candidate is test-only: the application's migrations embed excludes it.
//
//go:embed testdata/invoice_source_assignments.sql
var invoiceSourceRehearsalSQL string

const invoiceSourceRehearsalName = "test_only_invoice_source_assignments.sql"

// TestInvoiceSourceMigrationPreservesHistory rehearses provenance metadata without
// changing the stored invoice, source values, payments, ledger or account balance.
func TestInvoiceSourceMigrationPreservesHistory(t *testing.T) {
	pool := invoiceSourceRehearsalDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	seedInvoiceSourceRehearsal(t, pool)
	before := invoiceSourceRehearsalSnapshot(t, pool)
	if err := applyMigrationOnce(t.Context(), pool, invoiceSourceRehearsalName, invoiceSourceRehearsalSQL); err != nil {
		t.Fatalf("rehearse candidate: %v", err)
	}
	if after := invoiceSourceRehearsalSnapshot(t, pool); after != before {
		t.Fatalf("existing financial rows changed:\nbefore: %s\nafter: %s", before, after)
	}
	var balance string
	if err := pool.QueryRowContext(t.Context(), `SELECT (
		(SELECT SUM(gross) FROM invoices WHERE kind='invoice' AND status='issued')
		+(SELECT SUM(amount) FROM neighbor_ledger WHERE NOT voided)
		-(SELECT SUM(amount) FROM payments WHERE deleted_at IS NULL))::text`).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != "93.0000" {
		t.Fatalf("balance = %s, want 93.0000", balance)
	}
	var mapped, boundShares, selectedPayments int
	if err := pool.QueryRowContext(t.Context(), `SELECT count(*) FROM invoice_entries
		WHERE invoice_id=1 AND entry_id=1 AND source_entry_id=1 AND ordinal=1 AND active`).Scan(&mapped); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRowContext(t.Context(), `SELECT count(*) FROM beleg_shares WHERE invoice_id=1`).Scan(&boundShares); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRowContext(t.Context(), `SELECT count(*) FROM payments WHERE preferred_invoice_id IS NOT NULL`).Scan(&selectedPayments); err != nil {
		t.Fatal(err)
	}
	if mapped != 1 || boundShares != 1 || selectedPayments != 0 {
		t.Fatalf("mapping/share/payment preference = %d/%d/%d, want 1/1/0", mapped, boundShares, selectedPayments)
	}
	// A second document cannot claim a source already frozen by the first.
	rehearsalExec(t, pool, `INSERT INTO invoices (billing_year_id,neighbor_id,number,kind,status)
		VALUES (1,1,'2026-002','invoice','issued')`)
	if _, err := pool.ExecContext(t.Context(), `INSERT INTO invoice_entries
		(invoice_id,entry_id,source_entry_id,ordinal) VALUES (2,1,1,1)`); err == nil {
		t.Fatal("second active claim accepted for the same source")
	}
}

// TestInvoiceSourceMigrationRejectsAmbiguity proves failed rehearsal transactions
// leave both financial data and the complete old schema intact.
func TestInvoiceSourceMigrationRejectsAmbiguity(t *testing.T) {
	cases := []struct {
		name       string
		legacyCash bool
		mutate     string
	}{
		{"source cost mismatch", false, `UPDATE entries SET cost=101 WHERE id=1`},
		{"missing snapshot", false, `UPDATE invoices SET net=NULL WHERE id=1`},
		{"missing line quantity", false, `UPDATE invoices SET lines=lines #- '{0,qty}' WHERE id=1`},
		{"invalid line quantity", false, `UPDATE invoices SET lines=jsonb_set(lines,'{0,qty}','"invalid"') WHERE id=1`},
		{"malformed full line date", false, `UPDATE invoices SET lines=jsonb_set(lines,'{0,date}','"2026-05-01Tinvalid"') WHERE id=1`},
		{"numeric label matching live text", false, `
			UPDATE entries SET task_label='42' WHERE id=1;
			UPDATE invoices SET lines=jsonb_set(lines,'{0,label}','42') WHERE id=1`},
		{"fractional historical cash", true, `UPDATE payments SET amount=25.0001 WHERE id=1`},
		{"deleted fractional historical cash", true, `UPDATE payments SET amount=25.0001,deleted_at=now() WHERE id=1`},
		{"fractional historical ledger", true, `UPDATE neighbor_ledger SET amount=5.0001 WHERE id=1`},
		{"voided fractional historical ledger", true, `UPDATE neighbor_ledger SET amount=5.0001,voided=true WHERE id=1`},
		{"canceling fractional ledger pair", true, `
			UPDATE neighbor_ledger SET amount=0.005 WHERE id=1;
			INSERT INTO neighbor_ledger(billing_year_id,neighbor_id,amount,description)
			VALUES(1,1,-0.005,'Synthetic offsetting legacy sub-cent row')`},
		{"wrong credit owner", false, `
			INSERT INTO neighbors (name) VALUES ('Other synthetic neighbor');
			INSERT INTO invoices (billing_year_id,neighbor_id,number,kind,status,gross,references_invoice_id)
			VALUES (1,2,'2026-G001','gutschrift','issued',-10,1)`},
		{"excess attached credit", false, `
			INSERT INTO invoices (billing_year_id,neighbor_id,number,kind,status,gross,references_invoice_id)
			VALUES (1,1,'2026-G001','gutschrift','issued',-114,1)`},
		{"partial reversal", false, `
			INSERT INTO payments (billing_year_id,neighbor_id,amount,paid_on,reversal_of_payment_id)
			VALUES (1,1,-24,'2026-05-03',1)`},
		{"ambiguous legacy share", false, `
			INSERT INTO invoices (billing_year_id,neighbor_id,number,kind,status,created_at)
			VALUES (1,1,'2026-000','invoice','canceled','2026-01-01')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := invoiceSourceRehearsalDatabase(t)
			if tc.legacyCash {
				migrateBeforeCentsGuards(t, pool)
			} else if err := Migrate(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			seedInvoiceSourceRehearsal(t, pool)
			rehearsalExec(t, pool, tc.mutate)
			if tc.legacyCash {
				// Reproduce legal pre-0062 values, then install normal guards.
				// No trigger or integrity check is disabled for this fixture.
				if err := Migrate(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
			}
			before := invoiceSourceRehearsalSnapshot(t, pool)
			if err := applyMigrationOnce(t.Context(), pool, invoiceSourceRehearsalName, invoiceSourceRehearsalSQL); err == nil {
				t.Fatal("ambiguous historical fixture was accepted")
			}
			if after := invoiceSourceRehearsalSnapshot(t, pool); after != before {
				t.Fatalf("failed migration changed historical rows:\nbefore: %s\nafter: %s", before, after)
			}
			assertInvoiceSourceRehearsalRolledBack(t, pool)
		})
	}
}

// invoiceSourceRehearsalDatabase creates and owns a fresh database. The configured
// TEST_DATABASE_URL database is only the administrative connection, never a fixture.
func invoiceSourceRehearsalDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("TEST_INVOICE_MIGRATION_DATABASES") != "1" {
		t.Skip("disposable migration databases require TEST_DATABASE_URL and TEST_INVOICE_MIGRATION_DATABASES=1")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database configuration: %v", err)
	}
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	name := "treckrr_invoice_rehearsal_" + hex.EncodeToString(token[:])
	if cfg.Database == name {
		t.Fatal("disposable database must differ from configured database")
	}
	admin := stdlib.OpenDB(*cfg)
	if err := admin.PingContext(t.Context()); err != nil {
		_ = admin.Close()
		t.Fatalf("connect to disposable test server: %v", err)
	}
	identifier := pgx.Identifier{name}.Sanitize()
	// The identifier is generated locally and quoted, never supplied by a caller.
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identifier); err != nil {
		_ = admin.Close()
		t.Fatalf("create owned disposable database: %v", err)
	}
	child := cfg.Copy()
	child.Database = name
	pool := stdlib.OpenDB(*child)
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close disposable database: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Only the exact random name successfully created above is removed.
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+identifier); err != nil {
			t.Errorf("drop owned disposable database %s: %v", name, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close administrative connection: %v", err)
		}
	})
	var connected string
	if err := pool.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&connected); err != nil {
		t.Fatal(err)
	}
	if connected != name {
		t.Fatalf("refuse fixture writes outside owned database: got %q", connected)
	}
	return pool
}

// migrateBeforeCentsGuards builds a real historical schema before inserting old
// precision values; Migrate subsequently installs every normal current safeguard.
func migrateBeforeCentsGuards(t *testing.T, pool *sql.DB) {
	t.Helper()
	rehearsalExec(t, pool, `CREATE TABLE schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.IsDir() || file.Name() >= "0062_" {
			continue
		}
		content, err := migrationFS.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigrationOnce(t.Context(), pool, file.Name(), string(content)); err != nil {
			t.Fatal(err)
		}
	}
}

// seedInvoiceSourceRehearsal writes synthetic rows only to its caller-owned DB.
func seedInvoiceSourceRehearsal(t *testing.T, pool *sql.DB) {
	t.Helper()
	rehearsalExec(t, pool, `
		INSERT INTO price_bases(year,name) VALUES (2026,'Synthetic migration basis');
		INSERT INTO billing_years(year,base_id,label) VALUES(2026,1,'Synthetic migration year');
		INSERT INTO neighbors(name,address) VALUES('Synthetic migration neighbor','Synthetic address');
		INSERT INTO billing_year_neighbors(billing_year_id,neighbor_id) VALUES(1,1);
		INSERT INTO entries(neighbor_id,billing_year_id,entry_date,task_label,tractor_label,load_label,
		                    hours,hourly_rate,cost,unit,quantity,unit_price)
		VALUES(1,1,'2026-05-01','Synthetic work','','',0,0,100,'Pauschale',1,100);
		INSERT INTO invoices(billing_year_id,neighbor_id,number,issued_on,created_at,
		                     net,vat_rate,vat_amount,gross,show_vat,tax_mode,tax_note,
		                     service_from,service_to,issuer,recipient,lines,content_hash,payment_reference)
		VALUES(1,1,'2026-001','2026-05-02','2026-05-02',100,13,13,113,true,'pauschal','Synthetic tax notice',
		       '2026-05-01','2026-05-01','{"name":"Synthetic issuer","address":"Synthetic address"}',
		       '{"name":"Synthetic migration neighbor","address":"Synthetic address"}',
		       '[{"date":"2026-05-01T00:00:00Z","label":"Synthetic work","unit":"Pauschale", "qty":"1","unit_price":"100","cost":"100"}]',
		       'synthetic-frozen-hash','2026-001');
		INSERT INTO payments(billing_year_id,neighbor_id,amount,paid_on,invoice_id,note,method)
		VALUES(1,1,25,'2026-05-03',1,'Synthetic partial payment','bank');
		INSERT INTO neighbor_ledger(billing_year_id,neighbor_id,amount,posting_date,description)
		VALUES(1,1,5,'2026-05-03','Synthetic ledger adjustment');
		INSERT INTO beleg_shares(token_hash,neighbor_id,billing_year_id,expires_at,created_at)
		VALUES('synthetic-not-a-real-token',1,1,now()+interval '1 day','2026-05-04');`)
}

// invoiceSourceRehearsalSnapshot excludes only the new nullable/default metadata.
func invoiceSourceRehearsalSnapshot(t *testing.T, pool *sql.DB) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRowContext(t.Context(), `SELECT jsonb_build_object(
		'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM entries e),
		'invoices',(SELECT jsonb_agg(to_jsonb(i)-'issue_request_hash' ORDER BY id) FROM invoices i),
		'payments',(SELECT jsonb_agg(to_jsonb(p)-'preferred_invoice_id' ORDER BY id) FROM payments p),
		'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM neighbor_ledger l),
		'shares',(SELECT jsonb_agg(to_jsonb(s)-'invoice_id' ORDER BY id) FROM beleg_shares s),
		'sends',(SELECT jsonb_agg(to_jsonb(s)-'invoice_id' ORDER BY id) FROM beleg_sends s),
		'outbox',(SELECT jsonb_agg(to_jsonb(m)-'invoice_id' ORDER BY id) FROM mail_outbox m)
	)::text`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertInvoiceSourceRehearsalRolledBack(t *testing.T, pool *sql.DB) {
	t.Helper()
	var dirty bool
	if err := pool.QueryRowContext(t.Context(), `SELECT
		to_regclass('invoice_entries') IS NOT NULL
		OR to_regclass('invoices_active_per_neighbor_year') IS NULL
		OR EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)
		OR EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public'
		 AND ((table_name='invoices' AND column_name='issue_request_hash')
		 OR (table_name='payments' AND column_name='preferred_invoice_id')
		 OR (table_name IN ('beleg_shares','beleg_sends','mail_outbox') AND column_name='invoice_id')))`,
		invoiceSourceRehearsalName).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("migration failure left partial DDL or a schema version behind")
	}
	// Prove the former invariant remains enforced, not just that its index exists.
	_, err := pool.ExecContext(t.Context(), `INSERT INTO invoices
		(billing_year_id,neighbor_id,number) VALUES(1,1,'2026-rejected-duplicate')`)
	if err == nil || !strings.Contains(err.Error(), "invoices_active_per_neighbor_year") {
		t.Fatalf("old active-invoice uniqueness was not preserved: %v", err)
	}
}

func rehearsalExec(t *testing.T, pool *sql.DB, query string) {
	t.Helper()
	if _, err := pool.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}
