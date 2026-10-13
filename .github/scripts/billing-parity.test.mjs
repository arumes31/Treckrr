import assert from 'node:assert/strict';
import test from 'node:test';
import { compareBillingParity, differences } from './billing-parity-compare.mjs';
import { sameDaySnapshots } from './billing-parity-clock.mjs';

// Synthetic contract fixtures, deliberately independent of the comparator's
// expected-value construction. No database or generated financial report needed.
function fixture() {
  const cases = [
    ['small_business_unissued', 'initial', '100'], ['small_business_unpaid'], ['flat_rate_13_unpaid'],
    ['standard_20_unpaid'], ['standard_20_partial'], ['standard_20_fully_paid'],
    ['standard_20_overpaid_and_payout'], ['standard_20_overpaid_and_payout', 'after_payout'],
    ['manual_credit'], ['manual_debit', 'initial', '20'], ['debit_beyond_paid_document', 'initial', '10'],
    ['incoming_service'], ['credit_and_partial_payment'], ['storno_and_reissue_changed_tax'],
    ['storno_and_reissue_changed_tax', 'after_storno', '70'], ['storno_and_reissue_changed_tax', 'after_reissue'],
    ['skonto_two_percent'], ['deleted_payment_and_restore'], ['deleted_payment_and_restore', 'after_delete'],
    ['deleted_payment_and_restore', 'after_restore'], ['closed_year_settle_remaining'],
    ['closed_year_settle_remaining', 'after_close_and_settle'], ['carry_debit'], ['carry_debit', 'source_after_carry'],
    ['carry_debit', 'target_after_carry', '90'], ['carry_credit'], ['carry_credit', 'source_after_carry'],
    ['carry_credit', 'target_after_carry'], ['legacy_subcent_booking', 'initial', '10'], ['voided_booking_before_issue'],
  ];
  const before = cases.map(([scenario, checkpoint = 'initial']) => ({
    scenario, checkpoint, year: 2025, remaining: '100', documents: [{ hash: 'synthetic-unchanged-hash' }],
    closing: Object.fromEntries(['openanzahlung', 'uninvoiced', 'unpaid', 'unsent'].map(key =>
      [key, { amount: '0', count: 0 }])),
    dunning: [{ due: '2025-09-24', number: '2025-001', open: '100' }],
  }));
  for (const [scenario, amount] of [['manual_debit', '120'], ['debit_beyond_paid_document', '10']]) {
    const row = before.find(row => row.scenario === scenario);
    row.closing.unpaid = { amount, count: 1 };
    row.dunning[0].open = amount;
  }
  const after = structuredClone(before);
  after.forEach((row, index) => {
    const amount = cases[index][2] || '0';
    row.closing.account_unpaid = { amount, count: amount === '0' ? 0 : 1 };
    if (row.scenario === 'manual_debit') {
      row.closing.unpaid = { amount: '100', count: 1 };
      row.dunning[0].open = '100';
    }
    if (row.scenario === 'debit_beyond_paid_document') {
      row.closing.unpaid = { amount: '0', count: 0 };
      row.dunning = [];
    }
  });
  const names = ['Synthetic upgrade A credited', 'Synthetic upgrade B ledger residual',
    'Synthetic upgrade C paid and sent', 'Synthetic upgrade D unbilled'];
  const warning = (amount, ids) => ({ amount, count: ids.length, names: ids.map(id => names[id - 1]),
    neighbors: ids.map(ID => ({ ID, Name: names[ID - 1] })) });
  const upgradeBefore = [{ fixture: 'synthetic-upgrade', financial_rows: { invoices: 'unchanged' },
    closing: { openanzahlung: warning('0', []), uninvoiced: warning('0', [4]),
      unpaid: warning('83', [1, 2]), unsent: warning('0', [1, 2]) },
    dunning: [{ neighbor_id: 1, invoice: '2026-001', open: '73', due_on: '2026-01-29' },
      { neighbor_id: 2, invoice: '2026-002', open: '10', due_on: '2026-01-29' }] }];
  const upgradeAfter = structuredClone(upgradeBefore);
  upgradeAfter[0].closing.unpaid = warning('73', [1]);
  upgradeAfter[0].closing.account_unpaid = warning('17', [2, 4]);
  upgradeAfter[0].closing.unsent = warning('0', [1, 2, 3]);
  upgradeAfter[0].dunning.pop();
  return { before, after, upgradeBefore, upgradeAfter };
}

test('accepts only the reviewed matrix and upgrade transformations', () => {
  const result = compareBillingParity(fixture());
  assert.deepEqual(result.financialDifferences, { matrix: [], upgrade: [] });
  assert.deepEqual(result.unexpectedDifferences, { matrix: [], upgrade: [] });
  assert.ok(result.matrixDifferences.length > 0);
  assert.ok(result.upgradeDifferences.length > 0);
});

const mutations = {
  'annual money': data => { data.after[0].remaining = '99.99'; },
  'frozen hash': data => { data.after[0].documents[0].hash = 'changed'; },
  'warning count': data => { data.after[0].closing.account_unpaid.count = 2; },
  'warning amount': data => { data.after[0].closing.account_unpaid.amount = '100.01'; },
  'missing warning': data => { delete data.after[1].closing.account_unpaid; },
  'extra warning': data => { data.after[1].closing.extra = { count: 1, amount: '1' }; },
  'dunning due date': data => { data.after[1].dunning[0].due = '2025-09-25'; },
  'dunning amount': data => { data.after[9].dunning[0].open = '120'; },
  'extra dunning row': data => { data.after[10].dunning.push({ open: '10' }); },
  'missing dunning row': data => { data.after[1].dunning = []; },
  'upgrade invoice rows': data => { data.upgradeAfter[0].financial_rows.invoices = 'changed'; },
  'upgrade annual sent marker': data => { data.upgradeAfter[0].closing.unsent.count = 2; },
  'upgrade neighbor attribution': data => { data.upgradeAfter[0].closing.account_unpaid.neighbors[0].ID = 3; },
  'upgrade dunning metadata': data => { data.upgradeAfter[0].dunning[0].due_on = '2026-01-30'; },
};
for (const [name, mutate] of Object.entries(mutations)) {
  test(`rejects unexpected ${name}`, () => {
    const data = fixture();
    mutate(data);
    const result = compareBillingParity(data);
    assert.ok(result.unexpectedDifferences.matrix.length + result.unexpectedDifferences.upgrade.length > 0);
  });
}

for (const [name, mutate] of Object.entries({
  'missing case in both runtimes': data => { data.before.pop(); data.after.pop(); },
  'duplicate case': data => { data.after[1] = data.after[0]; },
  'changed baseline': data => { data.before[9].closing.unpaid.amount = '121'; },
  'missing upgrade': data => { data.upgradeAfter = []; },
})) {
  test(`rejects ${name} instead of weakening coverage`, () => {
    const data = fixture();
    mutate(data);
    assert.throws(() => compareBillingParity(data), /Unexpected baseline or matrix shape/);
  });
}

test('object property order is not a financial difference', () => {
  assert.deepEqual(differences({ a: '1', b: '2' }, { b: '2', a: '1' }), []);
});

test('same-day matrix runs exactly once', async () => {
  const calls = [];
  const result = await sameDaySnapshots(async attempt => {
    calls.push(attempt);
    return { before: 'old', after: 'new' };
  }, () => '2026-10-13');
  assert.deepEqual(calls, [0]);
  assert.deepEqual(result, { before: 'old', after: 'new', matrixUTCDate: '2026-10-13', matrixAttempts: 1 });
});

test('midnight retries BOTH snapshots, without mixing days', async () => {
  const dates = ['2026-10-13', '2026-10-14', '2026-10-14', '2026-10-14'];
  const calls = [];
  const result = await sameDaySnapshots(async attempt => {
    calls.push(attempt);
    return { before: [{ remaining: `${attempt}` }], after: [{ remaining: `${attempt}` }] };
  }, () => dates.shift());
  assert.deepEqual(calls, [0, 1]);
  assert.deepEqual(result.before, [{ remaining: '1' }]);
  assert.deepEqual(result.after, [{ remaining: '1' }]);
  assert.equal(result.matrixAttempts, 2);
});

test('a second date boundary fails instead of accepting incomparable snapshots', async () => {
  const dates = ['2026-10-13', '2026-10-14', '2026-10-14', '2026-10-15'];
  await assert.rejects(sameDaySnapshots(async () => ({ before: [], after: [] }), () => dates.shift()), /repeatedly crossed/);
});

test('a date boundary cannot retry away a financial difference', async () => {
  const dates = ['2026-10-13', '2026-10-14'];
  let attempts = 0;
  await assert.rejects(sameDaySnapshots(async () => {
    attempts++;
    return { before: [{ remaining: '100' }], after: [{ remaining: '99.99' }] };
  }, () => dates.shift()), /Financial differences/);
  assert.equal(attempts, 1);
});

test('a probe failure is never swallowed by the midnight guard', async () => {
  let attempts = 0;
  await assert.rejects(sameDaySnapshots(async () => {
    attempts++;
    throw new Error('real regression');
  }), /real regression/);
  assert.equal(attempts, 1);
});
