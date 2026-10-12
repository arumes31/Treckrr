# Production-impact review

Generate the actual branch comparison (read-only; never fetches, merges or pushes):

```sh
node .github/scripts/release-impact.mjs --base main --head dev
node --test .github/scripts/release-impact.test.mjs
```

The output records both exact commit IDs, the merge base, changed migration,
configuration, deployment, billing and security paths, plus unmatched files.
Uppercase configuration names are hints from added **and removed** lines, not a
claim that a setting is newly required. Deleted files remain visible.

By default all three human conclusions are **OFFEN**. Before releasing, write a
local JSON review (e.g. `tmp/release-review.json`, not a secret-bearing config):

```json
{
  "baseCommit": "<full main commit from report>",
  "headCommit": "<full dev commit from report>",
  "productionImpact": "Describe data/workflow effects after inspecting the diff.",
  "manualSteps": "List actions, or explicitly confirm none after review.",
  "rollback": "State binary/schema compatibility and any restore restrictions."
}
```

Validate it with:

```sh
node .github/scripts/release-impact.mjs --review tmp/release-review.json --check
```

`--check` fails if any section is missing or the exact compared commits changed.
The reviewer may be the solo operator; this is not a four-eyes approval process.
No tool can infer from filenames alone that migrations are safe or that no manual
step is required. Always inspect automatically listed candidates and the remaining
diff. Report generation and approval do not trigger deployment.
