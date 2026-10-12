import { expect, test, type Page } from "@playwright/test";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Benutzername").fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();
}

async function settings(page: Page) {
  await page.goto("/profile");
  const box = page.locator("[data-favorite-sync]");
  await expect(box).toBeEnabled();
  await page.locator("[data-favorite-settings] summary").click();
  return box;
}

test("favorites stay local until opt-in, sync across devices and reject a stale user header", async ({ page, browser }) => {
  await login(page);
  let box = await settings(page);
  await expect(box).not.toBeChecked();
  const user = await page.locator('meta[name="user-id"]').getAttribute("content");
  const prefix = `treckrr:favorites:v1:${user}:`;
  let writes = 0;
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/account/favorites") writes++;
  });
  await page.goto("/?year=1");
  const row = page.locator('[data-favorite-list="neighbors"] [data-favorite-id]').first();
  const neighbor = await row.getAttribute("data-favorite-id");
  await row.locator("[data-favorite-toggle]").click();
  await expect(row.locator("[data-favorite-toggle]")).toHaveAttribute("aria-pressed", "true");
  expect(await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]"), prefix + "neighbor")).toContain(neighbor);
  box = await settings(page);
  expect(writes).toBe(0);

  const optedIn = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/account/favorites");
  await box.check();
  const state = await (await optedIn).json();
  expect(state.enabled).toBe(true);
  expect(state.items.neighbor).toContain(neighbor);
  await expect(page.locator("[data-favorite-sync-status]")).toContainText("abgeglichen");

  const csrf = await page.locator('meta[name="csrf-token"]').getAttribute("content");
  const wrongUser = await page.request.post("/account/favorites", {
    headers: { "X-CSRF-Token": csrf || "", "X-Preference-User": "999999" },
    data: { enabled: false },
  });
  expect(wrongUser.status()).toBe(409);

  const second = await browser.newContext({ baseURL: new URL(page.url()).origin });
  try {
    const other = await second.newPage();
    await login(other);
    const otherBox = await settings(other);
    await expect(otherBox).toBeChecked();
    expect(await other.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]"), prefix + "neighbor")).toContain(neighbor);
    await otherBox.uncheck();
    await expect(other.locator("[data-favorite-sync-status]")).toContainText("Nur auf diesem Gerät");
  } finally {
    await second.close();
    // Restore the opt-in setting even if a browser assertion failed midway.
    await page.request.post("/account/favorites", {
      headers: { "X-CSRF-Token": csrf || "", "X-Preference-User": user || "" }, data: { enabled: false },
    });
  }
});
