import { expect, test, type Locator, type Page } from "@playwright/test";

/** Checks actual surface paint instead of merely the presence of a CSS class. */
async function expectOpaque(locator: Locator) {
  await expect(locator).toBeVisible();
  const alpha = await locator.evaluate((node) => {
    const style = getComputedStyle(node);
    const opacity = (value: string) => {
      if (value === "transparent") return 0;
      const numbers = value.match(/[\d.]+/g)?.map(Number) || [];
      return numbers.length === 4 ? numbers[3] : numbers.length === 3 ? 1 : 0;
    };
    const colorAlpha = opacity(style.backgroundColor);
    // Summary headers deliberately use an opaque gradient over transparent
    // background-color. Every gradient stop must be opaque to count as a sheet.
    const stops = style.backgroundImage.startsWith("linear-gradient(")
      ? style.backgroundImage.match(/rgba?\([^)]+\)/g) || [] : [];
    const gradientAlpha = stops.length >= 2 ? Math.min(...stops.map(opacity)) : 0;
    return Math.max(colorAlpha, gradientAlpha) * Number(style.opacity);
  });
  expect(alpha, "operational surfaces must hide the decorative page background").toBeGreaterThanOrEqual(0.99);
}

async function expectFits(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
}

test("mobile core workflows retain readable surfaces and reachable dialogs", async ({ page }, testInfo) => {
  testInfo.setTimeout(60_000);
  const theme = testInfo.project.use.colorScheme as "light" | "dark";
  await page.addInitScript((value) => localStorage.setItem("treckrr-theme", value), theme);
  await page.goto("/login");
  await page.getByLabel("Benutzername").fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);

  // Read-only: desktop cases may have changed balances and invoice state.
  const pages = [
    ["basis", "/prices?base=1"],
    ["neighbors", "/neighbors"],
    ["booking", "/neighbors/1?year=1&view=booking"],
    ["payment", "/neighbors/1?year=1&view=payments"],
    ["year-close", "/years/1/abschluss"],
  ];
  const timings: { route: string; responseAndRenderMs: number }[] = [];
  for (const [name, route] of pages) {
    const started = Date.now();
    const response = await page.goto(route);
    expect(response?.status(), route).toBeLessThan(400);
    await expect(page.locator("main")).toBeVisible();
    timings.push({ route, responseAndRenderMs: Date.now() - started });
    await expectFits(page);
    const sheets = page.locator("main .card:visible, main .work-surface:visible, main .neighbor-directory:visible");
    expect(await sheets.count(), `${name}: no opaque content sheet rendered`).toBeGreaterThan(0);
    for (const sheet of await sheets.all()) await expectOpaque(sheet);
    await testInfo.attach(`${name}-${theme}`, {
      body: await page.screenshot({ fullPage: true, animations: "disabled" }), contentType: "image/png",
    });
  }
  await testInfo.attach("synthetic-navigation-timings", {
    body: JSON.stringify({ diagnosticOnly: true, fixture: "e2e/seed.sql", timings }, null, 2), contentType: "application/json",
  });
  await page.getByRole("button", { name: "Menü", exact: true }).click();
  await expectOpaque(page.locator("#drawer"));
  await expectFits(page);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Schnellsuche (Strg+K)", exact: true }).click();
  const search = page.locator(".cmdk__box");
  await expectOpaque(search);
  await testInfo.attach(`search-dialog-${theme}`, {
    body: await search.screenshot({ animations: "disabled" }), contentType: "image/png",
  });
  await expect(search).toHaveScreenshot("search-dialog.png", { animations: "disabled", maxDiffPixelRatio: 0.01 });
  await page.keyboard.press("Escape");
  await expect(search).not.toBeVisible();
});
