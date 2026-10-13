# Backup schedules, restore rehearsals and compatibility inventory

## Schedule-aware backup health

The existing backup badge evaluates volume and S3 independently against their
configured cron schedules. A successful weekly backup does not become stale
after one day. The expected run is the next cron occurrence after the last
successful run. Its grace period is 10% of that interval, bounded to five minutes
through two hours. The backup panel shows the expected run and warning deadline.
Disabled destinations are neutral; a never-successful configured destination is
not marked healthy. Failed runs and future success timestamps remain warnings.
This changes classification, not the backup schedule or retention.

## Optional automatic restore rehearsal

Rehearsals remain disabled unless both `BACKUP_REHEARSE_URL` and
`BACKUP_REHEARSE_CRON` are explicitly configured, together with the backup key.
For example, `0 5 1 * *` requests a monthly run at 05:00 in the scheduler timezone.
Provide an account permitted to create/drop scratch databases, preferably on a
separate test PostgreSQL server, and retain at least one local encrypted backup.
The initial scheduler tick catches up a never-run or overdue schedule.

The scheduler uses the existing cluster scheduler and memory-heavy work leases.
Admission, success and retry clocks survive restart. Failed or interrupted
attempts wait at least the existing retry backoff. An unavailable local dump is
a failure, not a successful rehearsal. The latest dump is decrypted and restored
only into a freshly generated scratch database using the existing rehearsal
implementation. The source database is never used as the restore destination.
Scratch contents contain real data: protect the test server accordingly.
This is not an authorization to run a rehearsal on an unconfigured target.
Shutdown cancels the worker; only short status settlement and scratch cleanup
continue independently. The existing advisory-lock sessions have no lease-loss
heartbeat: a lost database session can permit overlapping work until timeout.
The independently generated scratch names still prevent restoring over the
source or another rehearsal's database. Prefer a separate, resource-limited
rehearsal server and do not treat the lease as a hard memory-isolation boundary.

The backup panel records the last successful rehearsal, duration/table count,
and a failed/interrupted state. The existing backup notification picks up that
failure. Migration 0080 adds only nullable scheduler timestamps; historical
bookings, invoices, prices and backups are not rewritten.

## Core-operation measurements

The existing token-protected `/metrics` endpoint now additionally exposes
`treckrr_operation_duration_seconds` for exactly five operation labels:
`dashboard`, `neighbor_account`, `booking_save`, `pdf`, and `export`.
Labels come from a closed enum mapped from registered routes. Names, IDs, URLs,
query strings and booking contents never become labels. This is HTTP wall time,
including database work; it is not a per-query SQL profile. DB pool wait counters
and Go allocation/runtime gauges remain available alongside it.

Run the deterministic instrumentation microbenchmark locally with:

```sh
go test ./internal/metrics -run '^$' -bench BenchmarkObserveOperation -benchmem -count 5
```

Use the isolated E2E environment for application timings, never production
load generation. Compare identical synthetic fixtures and hardware. A timing
attachment is a diagnostic, not proof of a SQL bottleneck; no speculative index
or query rewrite is introduced by this change.

The real-handler scaling harness covers 25 and 1,000 synthetic bookings, three
requests each for dashboard, neighbor account, booking save, PDF and export:

```sh
TRECKRR_PERF_TESTS=1 TEST_DATABASE_URL=<disposable-test-database> go test -tags=integration ./internal/server -run TestCoreOperationScalingIntegration -count=1 -v
```

It reports HTTP wall time, process allocation delta, DB pool waits and response
bytes, not SQL statement counts. It creates and removes its own test fixtures.
Never point `TEST_DATABASE_URL` at development data or production. Normal tests
skip this optional measurement to avoid speed assertions on shared CI machines.

## Historical compatibility inventory

Administrators can open **Backup → Historische Datenformate prüfen**, or
`/admin/legacy-inventory`. The explicit read-only report returns aggregate counts
and can be exported as JSON. It does not run on every page or expose names or
booking amounts. Queries are bounded by a ten-second context deadline.

The report identifies old replay keys, label-only machine snapshots, manual
ledger rows, old equipment/person snapshots, recurring single-helper templates
and incomplete invoice snapshots. These are shapes, not automatically defects.
Existing adapters (`LedgerBooking.BookingPeople`, recurring legacy-reference
resolution and immutable invoice snapshot readers) remain authoritative.
Legacy and current recurring-helper formats intentionally differ when a master
person has been removed; merging those paths would silently change history.
No compatibility path is removed: a zero count in development does not prove
that production data or old encrypted backups no longer need it.

### Read-only invoice source check

From the inventory, select **Rechnungszuordnung prüfen** to compare active
invoice snapshots with their current, non-voided service entries. The check is
opt-in (`/admin/legacy-inventory?invoice_sources=1`), admin-only, and uses a
read-only repeatable-read transaction. It does not assign entries, issue or
cancel invoices, change payments, or apply a migration.

The report compares source line order, dates, labels, units, quantities, rates,
costs and the cent-rounded net total. Current issuer/recipient master data is
deliberately excluded, and canceled historical invoices are not remapped.
Only counts, internal record IDs and reason codes are returned, not customer
names, line text or monetary values. The optional JSON export includes the
source check; the ordinary inventory JSON remains unchanged.

A mismatch requires investigation against the original records, not rewriting
a frozen invoice to make the check pass. A matching report is a point-in-time
source comparison, not approval of the tax calculation, proof that a future
migration is safe, or a reservation against later changes. Rerun after any data
change. Historical snapshots do not contain source IDs; a match does not prove
original row identity or revalidate the stored content hash.
The check itself never enables multiple invoices. Migration 0081 and the
coordinated runtime introduce that model; after migration, this report compares
each invoice with its own assigned sources rather than the complete account.
See [Multiple-invoice rollout](multi-invoice-rollout.md) for deployment safeguards.

The recurring-format consolidation shares only the person-existence lookup.
Legacy single-helper templates still skip a removed person; current helper-list
templates keep their frozen name/rate and clear the missing optional person ID.
Characterization tests cover both the scheduled and immediate runners, including
empty/null lists and retries, without rewriting stored templates.
