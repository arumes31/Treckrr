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

### Direct before/after comparison

`node .github/scripts/billing-parity.mjs f85ca89` runs the same public-API probes
against the last pre-implementation runtime and the current working source. It
archives the old source without switching branches, creates uniquely named,
network-isolated PostgreSQL/test containers, and removes its containers, images
and cache afterwards. It never accepts an application database URL. Reports and
source hashes stay under gitignored `tmp/billing-parity/`.

The comparison covers 20 scenarios and 30 checkpoints: all three tax modes,
unissued work, partial/full/excess payment, payout, positive/negative ledger
postings, incoming work, credit notes, cancellation/reissue, changed tax settings,
Skonto, payment deletion/restoration, year-close/settlement, debit/credit carry,
voided bookings and legacy sub-cent precision. Fixed expected amounts are checked
independently of the old/new comparison.

| Scenario / annual account remainder | Before | After |
| --- | ---: | ---: |
| EUR 100 net plus 20% VAT, EUR 30 paid | EUR 90.00 | EUR 90.00 |
| Same invoice, EUR 150 paid | EUR -30.00 credit | EUR -30.00 credit |
| EUR 24 credit note and EUR 30 paid | EUR 66.00 | EUR 66.00 |
| EUR 117.60 paid with 2% Skonto | EUR 0.00 | EUR 0.00 |
| Cancel and reissue at 13%, existing EUR 30 cash retained | EUR 83.00 | EUR 83.00 |
| Close year, settle EUR 90, repeat settlement | EUR 0.00; no second payment | EUR 0.00; no second payment |
| EUR 100 invoice + EUR 20 account debit - EUR 110 cash | EUR 10.00 | EUR 10.00 |
| Unissued EUR 9.995 legacy booking | EUR 10.00; raw cost 9.995 | EUR 10.00; raw cost 9.995 |

A separate two-binary upgrade probe seeds four accounts with the old runtime,
then migrates that exact database with the new runtime and runs the normal
snapshot backfill. All 39 existing rows across 14 inspected tables retained every
old column, including IDs, timestamps, payment attribution and document hashes.
Account remainders remained EUR 73/10/0/7; annual cash remained EUR 220.75 and the
legacy unrounded annual open total remained EUR 89.995. New source/document
metadata is checked separately; existing financial columns are not normalized
or silently rewritten to make the comparison pass.

The first comparison exposed a missing closing warning for account debt outside
an already paid invoice. The correction keeps document debt separate and adds
**Zusätzliche offene Kontoposten**, without changing money or double-counting
invoice debt. Its links open the account's payments view. Mahnungen still use
only the relevant document's remainder: in the EUR 10 account-debit example,
the invoice itself is paid and must not be dunned for EUR 10.

Historical annual delivery records cannot prove which particular invoice was
sent. They remain unchanged in history, but the new document-scoped closing
check can request an explicit delivery confirmation again. This neither deletes
the old record nor automatically resends an invoice.

The runner fails on any financial-field difference. It also records every
reminder/checklist difference for explicit review and runs race-enabled migration,
store, server and template regressions. Tests prove these synthetic cases, not
that all possible production histories will migrate; the isolated real-history
rehearsal in the deployment checklist is still required.

The corrected comparison has **zero financial differences**. Its 35 matrix and
nine upgrade display-field differences are accounted for: the new account-rest
check (including empty states), the split between document and account debt, and
unbound historical delivery records. In the upgrade fixture, EUR 73 of document
debt plus EUR 17 of additional account debt now exposes the full rounded EUR 90
annual remainder; this is a warning/display correction, not new debt.

Final follow-up validation passed on 2026-10-13:

- Both old/new matrix runs and the same-database upgrade, with race detection.
- Migration preservation and all 24 invalid-history rollback cases.
- Complete race-enabled store (290.012 s), server (327.534 s) and web/template
  (22.636 s) suites, including the new account-warning regressions.
- Native `go test ./...`, integration-tagged `go vet`, formatting and diff checks;
  golangci-lint 2.13.1: zero issues.
- All **164 Playwright cases** (160 desktop, four mobile) passed with retries
  disabled, plus a separate eight-case invoice/session run. The new browser test
  verifies document EUR 0/account EUR 10, the warning's payment link, year closure,
  annual settlement to EUR 0 and unchanged frozen invoice lines. An initially
  premature test navigation was fixed to await the actual close redirect.

No production or existing development database was used. Owned test containers,
images and cache were removed; ignored local reports were retained. No screenshot
or generated database fixture is included in the commit.
