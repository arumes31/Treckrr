import { expect, test, type Locator, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

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
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
  const diagnostics = overflow > 1 ? await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("body *")].filter(node => {
      const box = node.getBoundingClientRect();
      return box.width > 0 && (box.right > innerWidth + 1 || box.left < -1);
    }).slice(0, 20).map(node => {
      const box = node.getBoundingClientRect();
      return { tag: node.tagName, id: node.id, classes: node.className, left: box.left, right: box.right, width: box.width };
    })) : [];
  expect(overflow, `horizontal overflow at ${page.url()}: ${JSON.stringify(diagnostics)}`).toBeLessThanOrEqual(1);
}

async function expectTouchTarget(locator: Locator) {
  await expect(locator).toBeVisible();
  const box = await locator.boundingBox();
  expect(box, "touch target must have visible geometry").not.toBeNull();
  expect(box!.width, "touch target width").toBeGreaterThanOrEqual(43.5);
  expect(box!.height, "touch target height").toBeGreaterThanOrEqual(43.5);
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
  }
  await testInfo.attach("synthetic-navigation-timings", {
    body: JSON.stringify({ diagnosticOnly: true, fixture: "e2e/seed.sql", timings }, null, 2), contentType: "application/json",
  });
  await page.getByRole("button", { name: "Menü", exact: true }).click();
  await expectOpaque(page.locator("#drawer"));
  await expectFits(page);
  await page.keyboard.press("Escape");
  const searchButton = page.getByRole("button", { name: "Schnellsuche (Strg+K)", exact: true });
  await searchButton.focus();
  await searchButton.click();
  const dialog = page.getByRole("dialog", { name: "Suche", exact: true });
  await expect(dialog).toHaveAttribute("aria-modal", "true");
  const search = page.locator(".cmdk__box");
  await expectOpaque(search);
  await expectFits(page);
  const input = dialog.getByRole("combobox", { name: "Suche", exact: true });
  await expect(input).toBeFocused();
  await expect(input).toBeEditable();
  await expect(input).toBeInViewport({ ratio: 1 });
  expect((await input.boundingBox())?.height, "mobile search input needs a 44px touch target").toBeGreaterThanOrEqual(44);
  await input.fill("Nachbarn");
  const result = dialog.getByRole("option", { name: /Nachbarn/ }).first();
  await expect(result).toBeVisible();
  await expect(result).toBeInViewport({ ratio: 1 });
  await expect(search).toBeInViewport({ ratio: 1 });
  const contrast = await new AxeBuilder({ page }).include(".cmdk__box").withRules(["color-contrast"]).analyze();
  expect(contrast.violations, "search input and results must remain readable in both themes").toEqual([]);
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(searchButton).toBeFocused();
});

test("narrow mobile booking keeps all utilities, tabs and account details reachable", async ({ page }, testInfo) => {
  testInfo.setTimeout(60_000);
  await page.setViewportSize({ width: 320, height: 844 });
  const theme = testInfo.project.use.colorScheme as "light" | "dark";
  await page.addInitScript(value => localStorage.setItem("treckrr-theme", value), theme);
  await page.goto("/login");
  await page.getByLabel("Benutzername").fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();

  // Password visibility controls belong to account settings, not the login.
  // Exercise presentation only: never fill or submit a password change.
  const passwordResponse = await page.goto("/account/password");
  expect(passwordResponse?.status()).toBe(200);
  const passwordFields = page.locator('form[action="/account/password"] .pwwrap');
  await expect(passwordFields).toHaveCount(3);
  for (const field of await passwordFields.all()) {
    const toggle = field.locator("[data-pw-toggle]");
    const input = field.locator("input");
    await expect(toggle).toHaveAccessibleName("Passwort anzeigen");
    await expectTouchTarget(toggle);
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-pressed", "true");
    await expect(toggle).toHaveAccessibleName("Passwort verbergen");
    await expect(input).toHaveAttribute("type", "text");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-pressed", "false");
    await expect(toggle).toHaveAccessibleName("Passwort anzeigen");
    await expect(input).toHaveAttribute("type", "password");
  }
  await expectFits(page);

  // Desktop workflow tests intentionally close year 1. Select an existing open
  // synthetic year instead of reopening it or changing any accounting records.
  await page.goto("/years");
  const openYearLink = page.locator(".year-row").filter({ has: page.locator(".status--neutral") }).locator(".year-row__main > a").first();
  await expect(openYearLink).toBeVisible();
  const yearID = new URL((await openYearLink.getAttribute("href"))!, page.url()).searchParams.get("year")!;
  expect(yearID).toMatch(/^\d+$/);
  const bookingResponse = await page.goto(`/neighbors/1?year=${yearID}&view=booking`);
  expect(bookingResponse?.status()).toBe(200);
  await expect(page.locator("[data-unified-booking]")).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("data-theme", theme);

  // Stress only the rendered title; never rename a neighbor or mutate records.
  await page.locator(".appbar__title").evaluate(node => {
    node.textContent = "Jahresabrechnung – Landwirtschaftlicher Nachbarschaftsbetrieb";
  });
  await page.evaluate(() => document.fonts.ready);
  const utilities = page.locator(".appbar__tools .appbar__icon");
  await expect(utilities).toHaveCount(5);
  for (const utility of await utilities.all()) {
    await expectTouchTarget(utility);
    await expect(utility).toBeInViewport({ ratio: 1 });
  }
  const headerGeometry = await page.locator(".appbar").evaluate(header => {
    const title = header.querySelector<HTMLElement>(".appbar__title")!;
    const tools = header.querySelector<HTMLElement>(".appbar__tools")!;
    const controls = [...tools.querySelectorAll(".appbar__icon")].map(node => node.getBoundingClientRect());
    return {
      titleClipped: title.scrollWidth > title.clientWidth + 1 || title.scrollHeight > title.clientHeight + 1,
      separateRows: title.getBoundingClientRect().bottom <= tools.getBoundingClientRect().top + 1,
      overlappingControls: controls.some((a, i) => controls.slice(i + 1).some(b =>
        Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 &&
        Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1)),
    };
  });
  expect(headerGeometry).toEqual({ titleClipped: false, separateRows: true, overlappingControls: false });
  await expectFits(page);

  const backup = page.locator("[data-bk-toggle]");
  await backup.click();
  await expect(backup).toHaveAttribute("aria-expanded", "true");
  await expectOpaque(page.locator("#bkPop"));
  await expect(page.locator("#bkPop")).toBeInViewport({ ratio: 1 });
  const popupBox = await page.locator("#bkPop").boundingBox();
  expect(popupBox!.x).toBeGreaterThanOrEqual(0);
  expect(popupBox!.x + popupBox!.width).toBeLessThanOrEqual(320);
  await expectFits(page);
  await page.keyboard.press("Escape");
  await expect(backup).toHaveAttribute("aria-expanded", "false");

  const tabs = page.locator(".account-workspace > .section-tabs");
  await expect(tabs.getByRole("link")).toHaveCount(4);
  for (const label of ["Übersicht", "Buchen", "Buchungen", "Zahlungen"]) {
    const tab = tabs.getByRole("link", { name: label, exact: true });
    await expectTouchTarget(tab);
    await expect(tab).toBeInViewport({ ratio: 1 });
  }
  expect(await tabs.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(1);
  await expect(tabs.getByRole("link", { name: "Buchen", exact: true })).toHaveAttribute("aria-current", "page");

  const summary = page.locator(".summary-card--booking");
  await expectOpaque(summary);
  expect(await summary.evaluate(node => getComputedStyle(node).backgroundImage)).toContain("gradient");
  const details = summary.locator(".summary-card__details");
  await expect(details).not.toHaveAttribute("open");
  await expect(details.getByRole("link", { name: "CSV Export", exact: true })).toBeHidden();
  const monetaryText = () => summary.evaluate(node =>
    [...(node.textContent || "").matchAll(/[−-]?\d[\d.]*,\d{2}\s*€/g)].map(match => match[0].replace(/\s+/g, " ")));
  const originalMoney = await monetaryText();
  expect(originalMoney.length).toBeGreaterThanOrEqual(4);
  const disclosure = details.locator("summary");
  await expectTouchTarget(disclosure);
  await disclosure.click();
  await expect(details).toHaveAttribute("open", "");
  await expect(details.getByRole("link", { name: "Beleg", exact: true })).toBeVisible();
  await expect(details.getByRole("link", { name: "CSV Export", exact: true })).toHaveAttribute("href", `/export/neighbor/1?year=${yearID}`);
  await expect(details.getByRole("link", { name: "CSV Export", exact: true })).toBeVisible();
  expect(await monetaryText()).toEqual(originalMoney);
  await expectFits(page);
  await disclosure.click();
  await expect(details).not.toHaveAttribute("open");
  expect(await monetaryText()).toEqual(originalMoney);

  // Other account views keep the full summary and immediately visible exports.
  await tabs.getByRole("link", { name: "Übersicht", exact: true }).click();
  await expect(page.locator(".summary-card__details")).toHaveCount(0);
  await expect(page.locator(".summary-card").getByRole("link", { name: "CSV Export", exact: true })).toBeVisible();
  await expectFits(page);
});
