# Reviewed visual references

These bounded dialog screenshots are generated with the digest-pinned Linux
Playwright image, not a developer's OS fonts. To deliberately update them:

```sh
node e2e/run.mjs --project=mobile-chromium-light --project=mobile-chromium-dark --project=mobile-webkit-light --project=mobile-webkit-dark --update-snapshots
```

Review every changed reference before committing. A 1% pixel tolerance allows
minor rasterization noise; opacity and overflow remain separate strict assertions.
Full-page screenshots are report attachments, never automatic reference updates.
