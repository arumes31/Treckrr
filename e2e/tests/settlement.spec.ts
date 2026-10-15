import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// This journey owns a synthetic year/account and never uses the Dev database.
test("itemized invoice, payment dialog, text and PNG exports agree", async ({ page }) => {
  test.setTimeout(90_000);
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
      writeText: async (text: string) => { (window as any).__copiedInvoice = text; },
      write: async (items: ClipboardItem[]) => {
        const blob = await items[0].getType("image/png");
        const bitmap = await createImageBitmap(blob);
        (window as any).__invoiceImage = { size: blob.size, width: bitmap.width, height: bitmap.height };
        bitmap.close();
      },
    } });
  });
  await page.goto("/login");
  await page.locator('[name="username"]').fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await Promise.all([page.waitForNavigation(), page.getByRole("button", { name: "Anmelden", exact: true }).click()]);
  await page.goto("/years");
  const create = page.locator('form[action="/years"]');
  const year = await create.locator('[name="year"]').inputValue();
  const csrf = await create.locator('[name="csrf_token"]').inputValue();
  const post = async (path: string, form: Record<string, string>) => {
    const response = await page.request.post(path, { form: { csrf_token: csrf, ...form } });
    expect(response.ok(), path).toBeTruthy();
    return response;
  };
  const response = await post("/years", { year, base_id: "1", label: "Synthetic settlement exports" });
  const yearID = new URL(response.url()).searchParams.get("year")!;
  const neighborName = `Synthetic settlement neighbor ${year}`;
  await post("/neighbors/create", { name: neighborName });
  await page.goto("/neighbors");
  const neighbor = page.locator(".neighbor-row").filter({ has: page.getByText(neighborName, { exact: true }) });
  const neighborID = (await neighbor.getAttribute("id"))!.replace("neighbor-", "");
  await post("/years/add-neighbor", { year_id: yearID, neighbor_id: neighborID });
  await post("/admin/company", { name: "Synthetic settlement issuer", address: "Testfeld 1\n4710 Testdorf", tax_mode: "kleinunternehmer", tax_note: "Synthetischer Rechnungsnachweis", vat_rate: "0", iban: "AT611904300234573201" });
  await post(`/neighbors/${neighborID}/update`, { name: neighborName, address: "Testfeld 2\n4710 Testdorf" });
  await post("/entries", { year_id: yearID, neighbor_id: neighborID, entry_date: `${year}-05-01`, unit: "Pauschale", quantity: "1", unit_price: "322.20", task_label: "Synthetic invoice work" });
  await post("/entries", { booking_form_version: "3", booking_direction: "in", booking_kind: "fixed", year_id: yearID, neighbor_id: neighborID, entry_date: `${year}-05-02`, amount: "44", task_label: "Synthetic counterservice", note: "PRIVATE COUNTERSERVICE NOTE" });
  await post(`/neighbors/${neighborID}/payments`, { year_id: yearID, amount: "100", paid_on: `${year}-05-03`, method: "bar", note: "PRIVATE CASH NOTE" });
  await page.goto(`/neighbors/${neighborID}/invoice/confirm?year=${yearID}`);
  await page.waitForLoadState("load");
  await page.getByRole("button", { name: "Jetzt festschreiben", exact: true }).click();
  const ok = page.locator("#confirmModal [data-modal-ok]");
  await expect(ok).toBeVisible();
  await Promise.all([page.waitForURL(/invoice_id=/), ok.click()]);
  const invoiceURL = page.url();
  const invoiceID = new URL(invoiceURL).searchParams.get("invoice_id")!;
  const frozen = await page.locator(".beleg--document > .tablewrap table").innerText();
  const lines = page.getByRole("region", { name: "Einzelnachweis Zahlung und Verrechnung", exact: true });
  await expect(lines).toContainText("Synthetic counterservice");
  await expect(lines).toContainText("44,00 €");
  await expect(lines).toContainText("100,00 €");
  await expect(page.locator(".beleg__payinfo strong")).toHaveText("Offen: 178,20 €");
  await expect(page.locator("#beleg")).not.toContainText("PRIVATE CASH NOTE");
  await expect(page.locator("#beleg")).not.toContainText("PRIVATE COUNTERSERVICE NOTE");

  const exports = page.locator("details.document-actions__group");
  await expect(exports).not.toHaveAttribute("open");
  await expect(exports.getByRole("button", { name: "Text kopieren", exact: true })).toBeHidden();
  await exports.getByText("Weitere Formate und Export", { exact: true }).click();
  await expect(exports).toHaveAttribute("open", "");

  for (const width of [390, 1366]) {
    await page.setViewportSize({ width, height: 900 });
    for (const theme of ["light", "dark"]) {
      await page.evaluate((theme) => document.documentElement.setAttribute("data-theme", theme), theme);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      const scan = await new AxeBuilder({ page }).include("#beleg").withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
      expect(scan.violations.map((violation) => violation.id)).toEqual([]);
      await page.getByRole("button", { name: "Text kopieren", exact: true }).click();
      const copied = await page.evaluate(() => (window as any).__copiedInvoice);
      expect(copied).toContain("Synthetic counterservice");
      expect(copied).toContain("178,20 €");
      expect(copied).not.toContain("PRIVATE CASH NOTE");
      await page.getByRole("button", { name: "Bild", exact: true }).click();
      await expect.poll(() => page.evaluate(() => (window as any).__invoiceImage?.size || 0)).toBeGreaterThan(1000);
      const image = await page.evaluate(() => (window as any).__invoiceImage);
      expect(image.width).toBeGreaterThanOrEqual(width === 390 ? 780 : 1000);
      expect(image.height).toBeGreaterThan(500);
      await page.evaluate(() => { delete (window as any).__invoiceImage; });
    }
  }
  await page.getByRole("link", { name: "Zahlung verbuchen", exact: true }).click();
  await expect(page.locator('[data-payment-invoice]')).toHaveValue(invoiceID);
  const panel = page.locator(`[data-payment-breakdown="${invoiceID}"]`);
  await expect(panel).toBeVisible();
  await panel.locator("summary").click();
  await expect(panel).toContainText("Synthetic counterservice");
  await page.locator('[data-payment-invoice]').selectOption("");
  await expect(panel).toBeHidden();
  await page.locator('[data-payment-invoice]').selectOption(invoiceID);
  await expect(panel).toBeVisible();
  await page.goto(invoiceURL);
  expect(await page.locator(".beleg--document > .tablewrap table").innerText()).toBe(frozen);
});
