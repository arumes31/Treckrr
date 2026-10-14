# Invoice-bearing flat rates

New shared booking forms use contract version 3. `Ich verrechne → Material /
Pauschale` creates an ordinary own-service entry with quantity 1 and unit
`Pauschale`. It uses the existing pricing, rounding, VAT, invoice, export and
recurring-entry paths. Editing the stored entry uses the existing quantity editor.

`Nachbar verrechnet` remains a separate signed account posting. It affects
settlement, not the frozen own-service invoice lines or their tax base.

## Historical compatibility

- No migration, backfill, automatic reclassification or data rewrite.
- Version 2 forms and queued submissions retain their original behavior.
- Historical structured ledger edits remain ledger edits even if a newer
  form version is submitted.
- Local drafts remember their form contract; pre-upgrade drafts without this
  metadata resume as version 2 with an explicit account-posting explanation.
- Copying a historical own flat-rate posting is an explicit new invoice-bearing
  service. The form warns that the source posting still exists; never copy the
  same charge merely to move it onto an invoice.
- Recurring rules retain their persisted source model. New own flat-rate rules
  use the ordinary entry model; existing ledger rules are not converted.

## Example with no VAT

Own work 277.20 + own material 45.00 - counter-service 44.00 - payment 100.00
leaves the same annual balance of 178.20.

- Historical contract: frozen invoice 277.20, invoice remaining 177.20,
  additional account residual 1.00.
- New contract: invoice 322.20, counter-service offset 44.00 and payment 100.00,
  invoice remaining 178.20.

Where VAT applies, new own flat rates use the same net-input tax rules as other
own services. For example, a 45.00 net flat rate at 13% becomes 50.85 gross.
Historical ledger amounts and issued invoice snapshots are not repriced.

## Invoice versus account

Authenticated invoice pages show a separate, non-printing account comparison:
all open invoice balances + uninvoiced work/account residual = annual balance.
Its components and selected invoice balance use one read-only repeatable-read
snapshot. Public invoice links, PDFs and mail remain document-scoped and do not
gain access to the annual comparison. Payments retain explicit selection/FIFO
allocation and surplus credit behavior.

## Validation

### Itemized settlement supplement

Customer invoice HTML, PDF, email, shared links and XML comments now explain
applied reductions individually, separately from immutable invoice positions.
The same projection feeds reminders, payment-history allocation details and the
new authenticated `settlement.csv` export. A selected invoice CSV contains only
that invoice; the annual payment page exports all active invoices in the account.

Payment portions come directly from the existing allocator. Attached credits
retain their exact document reference. Pooled offsets are itemized only when
their attribution is unambiguous; mixed-sign ledger net and ambiguous partial
allocations are explicitly labelled as combined account offsets. No source date
or historical allocation is invented. Private payment/account notes are never
copied to customer invoice supplements; structured counterservice task labels
may describe an unambiguously applied source.

No migration, monetary recalculation, payment-preference rewrite or invoice
snapshot change is introduced. Accounting/journal CSV, collective invoice PDF
and archive ZIP retain their original financial values. The archive actions are
labelled accordingly; they are not current payment demands. The multi-year
account PDF separately shows payments, remaining balances and payment history.

The new CSV records invoice gross/open once per invoice, with signed applied
reductions on separate detail rows. It does not replace booking or accounting
exports. Invoice PNG, text-copy and share actions include the same dated
supplement without the private annual-account comparison.

### Itemized settlement validation

The immediate pre-itemization baseline is `4ee39df`. Its 30 financial
scenario/checkpoint snapshots match without a difference allowlist. The
historical pre-migration rehearsal separately checks frozen invoice values,
payments, balances, reminders and closing warnings on the same database.

New regressions cover exact payment portions across multiple invoices, FIFO and
explicit selection, surplus, reversals, attached/standalone credits, historical
negative invoices, and ambiguous or mixed-sign account offsets. They also prove
that the read-only projections preserve financial/audit rows and archive bytes.
Public HTML/PDF, email attachments, XML, selected CSV and payment dialogs are
checked for agreement and for exclusion of private notes and unrelated invoices.
Empty account pages for non-member years stay usable without granting document
or CSV access. The account statement's year totals and payment history share a
single read-only snapshot.

Browser checks exercise mobile/desktop layouts, both themes, accessibility,
actual PNG decoding, copied text, selected-payment disclosures, and the existing
book/close/mark-paid flow. PDF checks include all 120 individual rows, repeated
page headings, measured wrapping and visual inspection of every rendered page.

Validation completed with 168 full-suite browser tests and 10 affected-workflow
tests repeated after the empty-account fix. Full race/integration/migration
suites pass on PostgreSQL 16 and 18, with 64.2% overall statement coverage
(CI floor: 50%). Vet, pinned CI lint/security/deadcode checks, generated-asset
verification, module verification/tidiness, 40 workflow-script tests, staged
secret scanning and a Linux ARM64 build pass. Allocation fuzzing completed
47,167 executions, and the corrected background-drain test passed 100
race-enabled repetitions. All databases and artifacts used for this validation
are disposable/synthetic or remain under ignored `tmp/`; existing Dev and
production data are not accessed or changed.

### Earlier flat-rate validation

The pre-change comparison uses commit `1314437c586a8542d0b7af56fc8a34452fd83efd`.
All 30 existing financial scenario/checkpoint snapshots match the final source
exactly, including invoice contents/hashes, entries, account postings, payments,
annual balances, reminders and closing checks. No difference allowlist is used
for this immediate-baseline comparison. The separate historical migration
rehearsal also passes against the workflow's fixed pre-migration baseline.

Regression coverage includes:

- V2/V3 own and incoming flat rates; repeated submissions and rejected
  cross-contract retries without duplicate or reclassified financial rows.
- Historical ledger edits and actual saves of pre-upgrade browser drafts.
- New flat rates with helpers, existing VAT rules, and ordinary quantity edits.
- Invoice/account reconciliation, surplus credit, read-only row preservation,
  public-link isolation, and the book/close/mark-paid workflow.

The complete browser suite passed 167 tests, followed by 38 affected-workflow
tests on the final source. Eighteen additional real-template UI cases cover
light/dark themes, three viewport widths, accessibility, print exclusion and
public-link isolation. Full race/integration/migration suites pass on PostgreSQL
16 and 18. Unit tests, vet, pinned CI lint/security/deadcode tools, module
verification/tidiness, 40 workflow-script tests and a Linux ARM64 build pass.
A bounded allocation fuzz run completed 127,026 executions without failure.
The coverage-instrumented race/integration run reaches 63.8% overall statement
coverage (CI minimum: 50%). A secret scan of the staged change has no findings.

All database checks use disposable synthetic databases. Existing Dev and
production data are not used or changed. Runtime reports/screenshots remain
under ignored `tmp/`; none are part of the commit. GitHub-hosted checks must
still run after the user syncs the commit; local results do not replace them.
