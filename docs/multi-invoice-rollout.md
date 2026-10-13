# Multiple invoices per neighbor and year

## Workflow and unchanged values

An open billing year can contain several independent invoices for one neighbor.
Issuance freezes only non-voided entries not assigned to another active invoice.
Already invoiced entries, their amounts and the stored document content remain
unchanged. New services do not require payment or cancellation of earlier invoices.

The familiar workflow remains **book → close year → mark paid**. Closing prevents
new bookings and documents; payments and settlement remain available. “Gesamten
Rest bezahlen” settles the whole current annual balance, not merely the selected
invoice. Repeating it at a zero balance creates no additional payment.

- No invoice selected: payments go to the oldest open invoice, ordered by issue
  date and ID. Excess is available account credit; existing payment rows are not
  rewritten when later invoices appear.
- Explicit invoice selected: that invoice is prioritized, then any excess proceeds
  oldest-first. Skonto requires selection when several active invoices exist.
- Cancellation releases only that invoice's source entries and reverses its
  attached credit notes. Other invoices remain unchanged. Cash is retained;
  a preference for the canceled document becomes available to FIFO again.
- The annual balance counts active invoice gross plus uninvoiced booking net,
  active credit notes and ledger amounts, minus actual payments, exactly once.
  Individual invoice balances exclude uninvoiced work and positive account-only
  ledger debits. They must not be substituted for the annual account balance.
- HTML, PDF, e-invoice, email and payment QR use the selected frozen document.
  A public link never grants access to another invoice or subsequent work.

## Finding the workflow in the UI

1. Open the neighbor in the current year and use **Buchen** as before, including
   after an intermediate invoice. Only already invoiced source entries stay locked.
2. On **Beleg**, choose **Neue Leistungen abrechnen & festschreiben** to preview
   and issue the currently uninvoiced work. Earlier invoices are not replaced.
3. Open a particular document from **Rechnungen** to see its frozen lines, PDF
   and its own remaining amount. The neighbor header still shows the annual account.
4. Under **Zahlungen → Zahlung verbuchen**, leave **Rechnung zuordnen** empty for
   FIFO, or select the intended invoice explicitly. Opening this form from a
   document does not automatically select that document.
5. **Gesamten Rest bezahlen** still settles the annual balance, including after
   year closure. It is deliberately independent of the optional invoice selection.

## Migration 0081

The migration adds source provenance, explicit payment preferences and document
identity for links/delivery. It does not reprice entries, replace invoice content,
change existing payments, or repair balances. The legacy payment `invoice_id`
remains historical attribution; only new explicit choices use
`preferred_invoice_id`.

Legacy active invoices must match the exact ordered source projection, including
dates, labels, units, quantities, rates, costs and rounded net. Missing snapshots,
ambiguous assignments or links, invalid cash reversals, incompatible sub-cent
cash/ledger rows and invalid dates abort the entire migration transaction. The
old single-active-invoice constraint remains if migration fails. No partial source
mapping is retained and there is no automatic financial repair.

Canceled legacy documents without a complete snapshot remain incomplete; startup,
restore and journal export must not invent their content from current bookings.
Historical payment terms were not persisted: due-date displays retain the existing
neighbor override/company default policy rather than treating zero as a newly
frozen immediate deadline.

## Deployment checklist

1. Keep a verified restorable backup. Rehearse the new application against an
   isolated copy before deployment; a clean development fixture is not evidence
   that production history is unambiguous.
2. Use **Backup → Historische Datenformate prüfen → Rechnungszuordnung prüfen**
   for the read-only source report. Investigate discrepancies against original
   records; do not edit invoices merely to make the report pass. The report is
   useful evidence, not a substitute for rehearsing the full migration.
3. Deploy schema and runtime together, with no mixed old/new application writers.
   Application startup automatically runs 0081. On failure, leave the original
   data intact and investigate the reported IDs; do not disable checks.
4. Verify an old invoice, its annual balance and payment history. Then exercise
   new work, a second invoice, a partial payment and annual settlement in a test
   account/environment. Confirm that no old document changed.

No new environment variables are required. Old stateless HMAC links cannot prove
their original invoice identity and now fail closed; create a new invoice-bound
link when needed. Existing row-backed links are bound only when their original
document is unambiguous. Historical annual delivery records remain annual and do
not falsely mark every new invoice as sent.

Do not roll an old application back onto a database containing multiple active
invoices: its single-invoice queries and locks no longer describe that data.
Coordinate application rollback with restoration of the matching backup, taking
care not to discard payments or work entered since the backup.

## Validation boundaries

Migration tests use newly created disposable databases and synthetic data. They
compare existing financial rows before/after and prove rollback for ambiguous
history. Runtime tests cover source ownership, repeated/concurrent issuance,
FIFO/explicit payments, surplus credit, reversals, cancellation, year closure,
document isolation and links. Browser tests use `node e2e/run.mjs`, which creates
its own isolated stack. Never point these mutation tests at production or an
existing development database.

### Completed local validation (2026-10-13)

- Linux/Go 1.27.2/PostgreSQL 16: every Go package passed race/integration tests
  across the full run and the final database/server/web regression rerun. Initial
  failures exposed incomplete test addresses, obsolete single-invoice/UI
  expectations and a stale generated CSS bundle; these were corrected and the
  affected packages rerun successfully. The store suite also passed in full.
- PostgreSQL 18.6: targeted race-enabled migration, allocation, document-isolation
  and book-close-settle regressions passed. Migration cases prove preservation of
  historical financial rows and atomic failure for 24 invalid-history scenarios.
- Playwright: all 163 configured browser cases passed (159 desktop, four mobile),
  with retries disabled and no skipped cases. A separate invoice/session run
  passed all seven cases. Actual invoice views were checked at 320/1280 px in
  light/dark themes; QA images remain local and ignored.
- Allocation fuzzing: 74,911 generated executions passed in the final 30-second
  run, preserving the monetary accounting invariants.
- Native Go unit/non-database tests, `go vet`, generated-asset verification,
  formatting and diff checks passed; golangci-lint 2.13.1 reported zero issues.
  Both changed workflows passed actionlint syntax/expression checks (external
  ShellCheck/Pyflakes analyzers were not installed).

The migration rehearsal is explicitly enabled in the existing disposable CI
database jobs. Local tests did not access production or alter the existing
development database, and the normal development application was not rebuilt.
These results do not replace a migration rehearsal against a protected copy of
the actual historical data before production rollout.
