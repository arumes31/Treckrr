# Reproducible browser tests

From the repository root (Node 22+ and Docker Compose required):

```sh
node e2e/run.mjs
```

This is also the GitHub Actions command. It builds the current application, uses
the same Playwright browser image, starts a fresh PostgreSQL database, applies the
application migrations, and loads `seed.sql`. The fixture contains synthetic data
only. Existing development and production data are **never reset or mounted**.
No local Go, npm install, browser installation, database password, or `.env` is
needed. First use downloads Docker images and dependencies.

The runner generates a random `treckrr-e2e-*` Compose project, uses a loopback-only
random app port, and removes only its own containers/network/volumes in `finally`.
Database storage is temporary memory. Tests access localhost inside the app's
network namespace so service workers have a real secure localhost context.
The normal `treckrr-app` / `treckrr-db` containers are not touched. An interrupted
Docker daemon or SIGKILL may leave that printed test project behind: inspect its
name, then run `docker compose -p <that-exact-test-project> -f compose.e2e.yml down -v`.
Never substitute the normal application project name.

Useful bounded runs (all still get fresh fixtures):

```sh
node e2e/run.mjs --project=chromium
node e2e/run.mjs --project=mobile-chromium-light --project=mobile-webkit-dark
node e2e/run.mjs tests/booking.spec.ts
```

The existing desktop coverage is preserved. The 38-page accessibility crawl is
split into six-page batches in each theme/width combination, so a retry does not
repeat a whole palette and a single shared deadline cannot expire mid-crawl.
Four additional projects exercise the
five mobile core pages in Chromium/WebKit, light/dark, plus open search/navigation
dialogs. They enforce opaque content surfaces and absence of horizontal overflow;
screenshots of each state are attached to the report for review. These semantic
visual contracts avoid a noisy full-page pixel baseline. Four bounded search-dialog
reference screenshots are compared with 1% pixel tolerance; update them deliberately
as described in `snapshots/README.md`. Other screenshots are evidence, not automatically
approved baselines. Also review them and test one real smartphone for large UI changes.

Reports and screenshot/trace attachments are written to the ignored directories
`tmp/e2e-runs/<test-project>/playwright-report` and its adjacent `test-results`;
a new run does not overwrite old evidence. They are outside Playwright's default
output trees, so a parallel host test cannot delete a Docker run's mount. CI
uploads these even on failure.
The machine-local, gitignored `audit_report.spec.ts` is not an application fixture
and is excluded from the browser image, just like its private report.
Only the mobile read-only suite blocks service workers. Dedicated offline/PWA tests
keep service workers enabled and retain their explicit activation waits.
Do not run tests manually against production URLs: desktop tests create data.

`DESIGN.md` is the human design source. Test contracts enforce its opaque sheets,
theme separation and mobile field usability. Generated `.impeccable` metadata is
not a runtime stylesheet or test oracle; stale generated files must not silently
replace these contracts.
