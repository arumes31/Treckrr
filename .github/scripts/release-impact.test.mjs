import { test } from 'node:test';
import assert from 'node:assert/strict';
import { classify, configCandidates, renderReport, reviewMatches } from './release-impact.mjs';

test('migrations, config, deployment, billing and unclassified changes remain visible', () => {
  const files = ['internal/db/migrations/0090_test.sql', '.env.example', 'internal/config/config.go', 'Dockerfile',
    'internal/store/invoices.go', 'internal/server/auth.go', 'internal/server/unusual.go'].map((path) => ({ path, status: 'M' }));
  const groups = classify(files);
  assert.equal(groups.migrations.length, 1);
  assert.equal(groups.configuration.length, 2);
  assert.equal(groups.deployment.length, 1);
  assert.equal(groups.billing.length, 1);
  assert.equal(groups.security.length, 1);
  assert.equal(groups.other.length, 1);
  const report = renderReport({ base: 'a', head: 'b', mergeBase: 'a', files, variables: [] });
  for (const file of files) assert.ok(report.includes(file.path));
  assert.match(report, /OFFEN/);
  assert.match(report, /keine Aussage/);
});

test('config scan includes additions and removals, ignores unchanged context and filenames', () => {
  assert.deepEqual(configCandidates('--- OLD_FILE\n+++ NEW_FILE\n unchanged OLD_CONTEXT\n-FOO_BAR=old\n+FOO_BAZ=new\n+FOO_BAR=new'), ['FOO_BAR', 'FOO_BAZ']);
});

test('human conclusions require exact commits and all three sections', () => {
  const review = { baseCommit: 'a', headCommit: 'b', productionImpact: 'Additive migration', manualSteps: 'No extra configuration after review.', rollback: 'Old binary compatibility checked.' };
  assert.equal(reviewMatches(review, 'a', 'b'), true);
  assert.equal(reviewMatches(review, 'a', 'c'), false);
  assert.equal(reviewMatches({ ...review, manualSteps: '' }, 'a', 'b'), false);
  assert.equal(reviewMatches(undefined, 'a', 'b'), false);
  const report = renderReport({ base: 'a', head: 'c', mergeBase: 'a', files: [], variables: [], review });
  assert.ok(!report.includes('No extra configuration'));
  assert.match(report, /OFFEN/);
});

test('filenames and review content cannot inject report markup', () => {
  const report = renderReport({ base: 'a', head: 'b', mergeBase: 'a', files: [{ status: 'D', path: 'odd\n<script>|`file' }], variables: [] });
  assert.ok(!report.includes('<script>'));
  assert.ok(!report.includes('odd\n'));
  assert.ok(report.includes('`D`'));
});
