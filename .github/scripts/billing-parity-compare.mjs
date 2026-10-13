import { isDeepStrictEqual } from 'node:util';

// This is a reviewed contract for the fixed pre-0081 fixture, not a blanket
// exclusion of reminder/checklist fields. New cases require an explicit review.
const checkpoints = {
  small_business_unissued: ['initial'],
  small_business_unpaid: ['initial'],
  flat_rate_13_unpaid: ['initial'],
  standard_20_unpaid: ['initial'],
  standard_20_partial: ['initial'],
  standard_20_fully_paid: ['initial'],
  standard_20_overpaid_and_payout: ['initial', 'after_payout'],
  manual_credit: ['initial'],
  manual_debit: ['initial'],
  debit_beyond_paid_document: ['initial'],
  incoming_service: ['initial'],
  credit_and_partial_payment: ['initial'],
  storno_and_reissue_changed_tax: ['initial', 'after_storno', 'after_reissue'],
  skonto_two_percent: ['initial'],
  deleted_payment_and_restore: ['initial', 'after_delete', 'after_restore'],
  closed_year_settle_remaining: ['initial', 'after_close_and_settle'],
  carry_debit: ['initial', 'source_after_carry', 'target_after_carry'],
  carry_credit: ['initial', 'source_after_carry', 'target_after_carry'],
  legacy_subcent_booking: ['initial'],
  voided_booking_before_issue: ['initial'],
};
const identities = Object.entries(checkpoints).flatMap(([scenario, values]) =>
  values.map(checkpoint => `${scenario}/${checkpoint}`));
const accountRemainders = {
  'small_business_unissued/initial': '100',
  'manual_debit/initial': '20',
  'debit_beyond_paid_document/initial': '10',
  'storno_and_reissue_changed_tax/after_storno': '70',
  'carry_debit/target_after_carry': '90',
  'legacy_subcent_booking/initial': '10',
};

export function differences(before, after, location = '') {
  if (isDeepStrictEqual(before, after)) return [];
  if (before && after && typeof before === 'object' && typeof after === 'object'
      && Array.isArray(before) === Array.isArray(after)) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])].sort();
    return keys.flatMap(key => differences(before[key], after[key], `${location}/${key}`));
  }
  return [{ path: location, before: before === undefined ? '<missing>' : before,
    after: after === undefined ? '<missing>' : after }];
}

function requireEqual(actual, expected, label) {
  if (!isDeepStrictEqual(actual, expected)) throw new Error(`Unexpected baseline or matrix shape: ${label}`);
}

function expectedMatrixRecord(before) {
  const expected = structuredClone(before);
  const key = `${before.scenario}/${before.checkpoint}`;
  requireEqual(Object.keys(before.closing).sort(), ['openanzahlung', 'uninvoiced', 'unpaid', 'unsent'], key);
  const amount = accountRemainders[key] || '0';
  expected.closing.account_unpaid = { amount, count: amount === '0' ? 0 : 1 };
  if (key === 'manual_debit/initial' || key === 'debit_beyond_paid_document/initial') {
    const debitOnly = key === 'debit_beyond_paid_document/initial';
    const oldOpen = debitOnly ? '10' : '120';
    requireEqual(before.closing.unpaid, { amount: oldOpen, count: 1 }, `${key}/unpaid`);
    requireEqual(before.dunning.length, 1, `${key}/dunning length`);
    requireEqual(before.dunning[0].open, oldOpen, `${key}/dunning open`);
    expected.closing.unpaid = { amount: debitOnly ? '0' : '100', count: debitOnly ? 0 : 1 };
    if (debitOnly) expected.dunning = [];
    else expected.dunning[0].open = '100';
  }
  return expected;
}

function upgradeWarning(amount, ids) {
  const names = {
    1: 'Synthetic upgrade A credited',
    2: 'Synthetic upgrade B ledger residual',
    3: 'Synthetic upgrade C paid and sent',
    4: 'Synthetic upgrade D unbilled',
  };
  return { amount, count: ids.length, names: ids.map(id => names[id]),
    neighbors: ids.map(ID => ({ ID, Name: names[ID] })) };
}

function expectedUpgradeRecord(before) {
  const expected = structuredClone(before);
  requireEqual(Object.keys(before.closing).sort(), ['openanzahlung', 'uninvoiced', 'unpaid', 'unsent'], 'upgrade closing');
  requireEqual(before.closing.unpaid, upgradeWarning('83', [1, 2]), 'upgrade unpaid');
  requireEqual(before.closing.unsent, upgradeWarning('0', [1, 2]), 'upgrade unsent');
  requireEqual(before.dunning.map(row => [row.neighbor_id, row.invoice, row.open]),
    [[1, '2026-001', '73'], [2, '2026-002', '10']], 'upgrade dunning');
  expected.closing.unpaid = upgradeWarning('73', [1]);
  expected.closing.account_unpaid = upgradeWarning('17', [2, 4]);
  // A legacy annual sent marker is not evidence for a specific frozen document.
  expected.closing.unsent = upgradeWarning('0', [1, 2, 3]);
  expected.dunning = expected.dunning.slice(0, 1);
  return expected;
}

function financialFields({ closing, dunning, ...financial }) {
  return financial;
}

export function financialDifferences(before, after) {
  return differences(before.map(financialFields), after.map(financialFields));
}

export function compareBillingParity({ before, after, upgradeBefore, upgradeAfter }) {
  const identity = rows => rows.map(row => `${row.scenario}/${row.checkpoint}`);
  requireEqual(identity(before), identities, 'before checkpoints');
  requireEqual(identity(after), identities, 'after checkpoints');
  requireEqual(upgradeBefore.length, 1, 'upgrade before length');
  requireEqual(upgradeAfter.length, 1, 'upgrade after length');
  return {
    matrixDifferences: differences(before, after),
    upgradeDifferences: differences(upgradeBefore, upgradeAfter),
    financialDifferences: {
      matrix: financialDifferences(before, after),
      upgrade: financialDifferences(upgradeBefore, upgradeAfter),
    },
    // Unlike financialDifferences, this gate includes EVERY field, after only
    // the narrow transformations above. Changed dates, counts, names, amounts,
    // extra/missing warnings and dunning rows all fail the comparison.
    unexpectedDifferences: {
      matrix: differences(before.map(expectedMatrixRecord), after),
      upgrade: differences(upgradeBefore.map(expectedUpgradeRecord), upgradeAfter),
    },
  };
}
