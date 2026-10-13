import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block", screenshot: "off", trace: "off" });

test("admin invoice-source check is explicit and preserves the ordinary inventory", async ({ page }) => {
  test.setTimeout(60_000);
  const runtimeErrors: string[] = [];
  page.on("pageerror", (error) => runtimeErrors.push(error.message));

  await page.goto("/login");
  await page.getByLabel("Benutzername").fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();

  await page.goto("/admin/legacy-inventory");
  const card = page.getByRole("region", { name: "Rechnungszuordnung vorbereiten" });
  await expect(card.getByRole("link", { name: "Rechnungszuordnung prüfen", exact: true })).toBeVisible();
  await expect(card).toContainText("Die zusätzliche Prüfung startet erst");
  await expect(card.getByText("Geprüfte Rechnungen", { exact: true })).toHaveCount(0);

  const ordinaryResponse = await page.request.get("/admin/legacy-inventory?format=json");
  expect(ordinaryResponse.status()).toBe(200);
  expect(ordinaryResponse.headers()["cache-control"]).toBe("no-store");
  const ordinary = await ordinaryResponse.json();
  expect(typeof ordinary.Entries).toBe("number");
  expect(ordinary).not.toHaveProperty("invoice_sources");
  expect(ordinary).not.toHaveProperty("inventory");

  await card.getByRole("link", { name: "Rechnungszuordnung prüfen", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/legacy-inventory\?invoice_sources=1$/);
  await expect(card.getByText("Geprüfte Rechnungen", { exact: true })).toBeVisible();
  await expect(card).toContainText("schaltet mehrere Rechnungen pro Jahr nicht frei");
  await expect(card).toContainText("Nach Änderungen muss die Prüfung erneut ausgeführt werden");
  await expect(card.getByRole("link", { name: "Prüfergebnis als JSON", exact: true })).toHaveAttribute(
    "href", "/admin/legacy-inventory?format=json&invoice_sources=1",
  );

  const checkResponse = await page.request.get("/admin/legacy-inventory?format=json&invoice_sources=1");
  expect(checkResponse.status()).toBe(200);
  expect(checkResponse.headers()["cache-control"]).toBe("no-store");
  const checked = await checkResponse.json();
  expect(checked.inventory).toEqual(ordinary);
  expect(checked.invoice_sources).toEqual({
    checked: expect.any(Number), matched: expect.any(Number),
    matched_entries: expect.any(Number), issues: expect.any(Array),
  });
  expect(checked.invoice_sources.checked).toBeGreaterThanOrEqual(checked.invoice_sources.matched);
  const repeated = await page.request.get("/admin/legacy-inventory?format=json&invoice_sources=1");
  expect(await repeated.json()).toEqual(checked);

  for (const theme of ["light", "dark"] as const) {
    await page.evaluate((value) => localStorage.setItem("treckrr-theme", value), theme);
    await page.emulateMedia({ colorScheme: theme });
    for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport);
      await page.reload();
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(card.getByText("Geprüfte Rechnungen", { exact: true })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
    }
  }
  expect(runtimeErrors).toEqual([]);
});
