import { expect, test } from "@playwright/test";

test("release evidence covers payment access, draft recovery and carry preview in both themes", async ({ page }, testInfo) => {
  testInfo.setTimeout(120_000);
  await page.goto("/login");
  await page.getByLabel("Benutzername").fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();

  // Separate synthetic years keep screenshots independent of the destructive
  // invoice/session tests. Only the disposable E2E database receives these rows.
  const ids: string[] = [];
  let sourceYear = "";
  for (let i = 0; i < 2; i++) {
    await page.goto("/years");
    const form = page.locator('form[action="/years"]');
    const year = await form.locator('[name="year"]').inputValue();
    if (i === 0) sourceYear = year;
    else expect(Number(year)).toBe(Number(sourceYear) + 1);
    const csrf = await form.locator('[name="csrf_token"]').inputValue();
    const created = await page.request.post("/years", { form: { csrf_token: csrf, year, base_id: "1", label: `Visual E2E ${year}` } });
    expect(created.ok()).toBeTruthy();
    const id = new URL(created.url()).searchParams.get("year")!;
    expect(id).toBeTruthy();
    ids.push(id);
    const assigned = await page.request.post("/years/add-neighbor", { form: { csrf_token: csrf, year_id: id, neighbor_id: "1" } });
    expect(assigned.ok()).toBeTruthy();
  }
  const account = `/neighbors/1?year=${ids[0]}`;
  await page.goto(account + "&view=booking");
  const form = page.locator("[data-unified-booking]");
  await form.locator('[name="booking_kind"]').selectOption("quantity");
  await form.locator('[name="entry_date"]').fill(sourceYear + "-05-01");
  await form.locator('[name="unit"]').selectOption("Ballen");
  await form.locator('[name="quantity"]').fill("3");
  await form.locator('[name="unit_price"]').fill("7");
  await form.locator('[name="task_label"]').fill("E2E Ballen transportieren");
  await page.getByRole("button", { name: "Buchung speichern", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`year=${ids[0]}&view=bookings`));

  await page.goto(account + "&view=booking");
  await form.locator('[name="booking_kind"]').selectOption("quantity");
  await form.locator('[name="task_label"]').fill("Ungespeicherte Feldarbeit");
  await page.reload();
  await expect(page.locator("[data-booking-draft-offer]")).toBeVisible();

  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await page.evaluate((theme) => localStorage.setItem("treckrr-theme", theme), theme);
    for (const [size, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
      await page.setViewportSize({ width, height });
      for (const [name, route] of [
        ["dashboard", `/?year=${ids[0]}`],
        ["neighbor-account", account],
        ["neighbor-overview", "/neighbors/1/overview"],
        ["draft-offer", account + "&view=booking"],
        ["carry-preview", `/neighbors/1/carry-forward?year=${ids[0]}`],
      ]) {
        const response = await page.goto(route);
        expect(response?.status(), route).toBe(200);
        await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
        if (name === "neighbor-account") await expect(page.getByRole("link", { name: "Zahlung verbuchen", exact: true })).toBeVisible();
        if (name === "draft-offer") await expect(page.locator("[data-booking-draft-offer]")).toBeVisible();
        if (name === "carry-preview") await expect(page.locator("main").getByRole("heading", { name: /Jahresübertrag prüfen/ })).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
        const screenshot = testInfo.outputPath(`${name}-${size}-${theme}.png`);
        await page.screenshot({ path: screenshot, fullPage: true, animations: "disabled" });
        await testInfo.attach(`${name}-${size}-${theme}`, { path: screenshot, contentType: "image/png" });
      }
    }
  }
});
