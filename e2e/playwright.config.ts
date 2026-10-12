import { defineConfig, devices } from "@playwright/test";

// e2e/run.mjs starts the same disposable Docker stack locally and in CI. The
// desktop suite stays intact; four bounded mobile projects cover core surfaces.
// A trace on first retry makes flakes diagnosable in the local report.
//
// workers: 1 — all specs share ONE database (there is no per-test DB isolation), so
// parallel workers would race and one spec's writes (a booking, an issued invoice)
// would leak into another's assertions (e.g. the a11y scan). Serial execution keeps
// the shared state deterministic. Mobile checks are read-only.
export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: process.env.BASE_URL || "http://localhost:8080",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "chromium", testIgnore: /mobile_core\.spec\.ts/, use: { ...devices["Desktop Chrome"] } },
    ...(["light", "dark"] as const).flatMap((colorScheme) => [
      {
        name: `mobile-chromium-${colorScheme}`,
        testMatch: /mobile_core\.spec\.ts/,
        use: { ...devices["Pixel 7"], colorScheme, serviceWorkers: "block" as const },
      },
      {
        name: `mobile-webkit-${colorScheme}`,
        testMatch: /mobile_core\.spec\.ts/,
        use: { ...devices["iPhone 13"], colorScheme, serviceWorkers: "block" as const },
      },
    ]),
  ],
});
