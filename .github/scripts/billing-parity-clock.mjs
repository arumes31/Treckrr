import { financialDifferences } from './billing-parity-compare.mjs';

// Cancellation stamps today's date. Comparing two separate runtimes across
// midnight would change the replacement invoice's due date without a regression.
// Retry the whole synthetic pair once, never weaken the date/money comparison.
export async function sameDaySnapshots(runPair, utcDate = () => new Date().toISOString().slice(0, 10)) {
  for (let attempt = 0; attempt < 2; attempt++) {
    const date = utcDate();
    const snapshots = await runPair(attempt);
    if (utcDate() === date) return { ...snapshots, matrixUTCDate: date, matrixAttempts: attempt + 1 };
    if (financialDifferences(snapshots.before, snapshots.after).length) {
      throw new Error('Financial differences in midnight-crossing matrix; refusing to retry away a regression');
    }
  }
  throw new Error('Billing matrix repeatedly crossed a UTC date boundary; no comparison accepted');
}
