import { test, expect, Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import path from "node:path";

const appScript = path.resolve(__dirname, "../../internal/web/static/js/app.js");
const fixture = `<!doctype html><html lang="de"><head><meta charset="utf-8"></head><body>
  <form method="post" action="/issue" data-batch-invoice data-confirm="Die ausgewählten Rechnungen jetzt endgültig festschreiben?">
    <label><input type="checkbox" name="neighbor_id" value="1" checked data-invoice-gross="12.34">Erster Hof</label>
    <input type="hidden" name="hash_1" value="hash-one">
    <label><input type="checkbox" name="neighbor_id" value="2" checked data-invoice-gross="0.10">Zweiter Hof</label>
    <input type="hidden" name="hash_2" value="hash-two">
    <label><input type="checkbox" name="neighbor_id" value="3" checked disabled data-invoice-gross="999.99">Gesperrter Hof</label>
    <span data-batch-fallback>bis zu 2 Rechnungen · Summe 12,44 € — ohne JavaScript die Summe aller wählbaren Rechnungen</span>
    <span data-batch-summary role="status" aria-live="polite" aria-atomic="true" hidden></span>
    <button type="submit" data-batch-submit data-confirm="Die ausgewählten Rechnungen jetzt endgültig festschreiben?">Ausgewählte festschreiben</button>
    <button type="reset">Zurücksetzen</button>
  </form>
  <dialog id="confirmModal"><form method="dialog">
    <p data-modal-msg></p>
    <button type="submit" value="cancel">Abbrechen</button>
    <button type="submit" value="confirm" data-modal-ok>Bestätigen</button>
  </form></dialog>
  <script src="/static/js/app.js"></script>
  </body></html>`;

// Deny-by-default fixtures exercise production script behavior without any
// development/production server, database, account, or invoice writes.
async function openFixture(page: Page, html = fixture) {
  page.on("pageerror", error => { throw error; });
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.pathname === "/fixture") return route.fulfill({ contentType: "text/html", body: html });
    if (url.pathname === "/static/js/app.js") {
      return route.fulfill({ contentType: "application/javascript", body: readFileSync(appScript, "utf8") });
    }
    return route.fulfill({ status: 404 });
  });
  await page.goto("http://workflow-polish.test/fixture");
}

test("batch totals and confirmation follow the selected eligible invoices", async ({ page }) => {
  await openFixture(page);
  const summary = page.locator("[data-batch-summary]");
  await expect(summary).toHaveText("2 Rechnungen ausgewählt · Summe brutto 12,44 €");
  await expect(page.locator("[data-batch-fallback]")).toBeHidden();
  await page.getByLabel("Erster Hof").uncheck();
  await expect(summary).toHaveText("1 Rechnung ausgewählt · Summe brutto 0,10 €");

  await page.getByRole("button", { name: "Ausgewählte festschreiben" }).click();
  await expect(page.locator("#confirmModal")).toBeVisible();
  await expect(page.locator("[data-modal-msg]")).toContainText("1 Rechnung ausgewählt · Summe brutto 0,10 €");
  await page.getByRole("button", { name: "Abbrechen", exact: true }).click();
  await expect(page.locator("[data-batch-submit]")).toBeEnabled();

  const posted: URLSearchParams[] = [];
  await page.route("**/issue", async route => {
    posted.push(new URLSearchParams(route.request().postData() || ""));
    return route.fulfill({ contentType: "text/html", body: "Test submission received" });
  });
  await page.getByRole("button", { name: "Ausgewählte festschreiben" }).click();
  await page.getByRole("button", { name: "Bestätigen", exact: true }).click();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0].getAll("neighbor_id")).toEqual(["2"]);
  expect(posted[0].get("hash_2")).toBe("hash-two");
  expect(posted[0].has("gross")).toBe(false);
});

test("empty selection cannot submit and reset restores the exact total", async ({ page }) => {
  await openFixture(page);
  await page.getByLabel("Erster Hof").uncheck();
  await page.getByLabel("Zweiter Hof").uncheck();
  await expect(page.locator("[data-batch-summary]")).toHaveText("0 Rechnungen ausgewählt · Summe brutto 0,00 €");
  await expect(page.locator("[data-batch-submit]")).toBeDisabled();
  await page.locator("[data-batch-invoice]").evaluate((form: HTMLFormElement) => form.requestSubmit());
  await expect(page.locator("#confirmModal")).not.toBeVisible();
  expect(page.url()).toContain("/fixture");
  await page.getByRole("button", { name: "Zurücksetzen" }).click();
  await expect(page.locator("[data-batch-summary]")).toHaveText("2 Rechnungen ausgewählt · Summe brutto 12,44 €");
  await expect(page.locator("[data-batch-submit]")).toBeEnabled();
});

test("restoring a page does not re-enable an empty batch", async ({ page }) => {
  await openFixture(page);
  await page.getByLabel("Erster Hof").uncheck();
  await page.getByLabel("Zweiter Hof").uncheck();
  await page.locator("[data-batch-submit]").evaluate((button: HTMLButtonElement) => {
    button.classList.add("is-submitting");
    button.form!.dataset.submitting = "1";
    button.form!.dataset.confirmed = "1";
    window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true }));
  });
  await expect(page.locator("[data-batch-submit]")).toBeDisabled();
  await expect(page.locator("[data-batch-invoice]")).not.toHaveAttribute("data-submitting");
  await expect(page.locator("[data-batch-invoice]")).not.toHaveAttribute("data-confirmed");
});

test("batch confirmation also applies when submission has no button submitter", async ({ page }) => {
  await openFixture(page);
  await page.locator("[data-batch-invoice]").evaluate((form: HTMLFormElement) => form.requestSubmit());
  await expect(page.locator("#confirmModal")).toBeVisible();
  await expect(page.locator("[data-modal-msg]")).toContainText("2 Rechnungen ausgewählt · Summe brutto 12,44 €");
});

test("batch cents remain exact beyond floating-point safe integer precision", async ({ page }) => {
  const large = fixture.replace('data-invoice-gross="12.34"', 'data-invoice-gross="90071992547409.91"');
  await openFixture(page, large);
  await expect(page.locator("[data-batch-summary]")).toHaveText("2 Rechnungen ausgewählt · Summe brutto 90.071.992.547.410,01 €");
});

test("malformed selected amount blocks issuing instead of displaying a misleading sum", async ({ page }) => {
  await openFixture(page, fixture.replace('data-invoice-gross="12.34"', 'data-invoice-gross="12,34"'));
  await expect(page.locator("[data-batch-summary]")).toHaveText("Auswahlbetrag nicht verfügbar. Bitte die Seite neu laden.");
  await expect(page.locator("[data-batch-submit]")).toBeDisabled();
  await page.getByLabel("Erster Hof").uncheck();
  await expect(page.locator("[data-batch-summary]")).toContainText("0,10 €");
  await expect(page.locator("[data-batch-submit]")).toBeEnabled();
});

test("without JavaScript the fallback stays an explicit selectable upper bound", async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, serviceWorkers: "block" });
  try {
    const page = await context.newPage();
    await openFixture(page);
    await page.getByLabel("Erster Hof").uncheck();
    await expect(page.locator("[data-batch-fallback]")).toHaveText(/bis zu 2 Rechnungen.*Summe aller wählbaren Rechnungen/);
    await expect(page.locator("[data-batch-summary]")).toBeHidden();
    await expect(page.locator("[data-batch-submit]")).toBeEnabled();
  } finally {
    await context.close();
  }
});
