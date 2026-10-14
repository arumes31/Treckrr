import { expect, test, type Locator, type Page } from "@playwright/test";

/** Waits for the actual POST/redirect completion, including same-URL redirects. */
async function submit(page: Page, button: Locator) {
  await Promise.all([page.waitForNavigation({ waitUntil: "load" }), button.click()]);
}

/** Confirms the real application dialog without bypassing mobile hit testing. */
async function confirm(page: Page) {
  const button = page.locator("#confirmModal [data-modal-ok]");
  await expect(button).toBeVisible();
  await submit(page, button);
}

type FrozenDocument = { url: string; id: string; header: string; lines: string; hash: string };

/** Captures only immutable invoice fields; the payment remainder must change. */
async function frozenDocument(page: Page): Promise<FrozenDocument> {
  const id = new URL(page.url()).searchParams.get("invoice_id");
  expect(id).toMatch(/^\d+$/);
  const hash = (await page.locator("#beleg details").filter({
    has: page.getByText("Prüfsumme", { exact: true }),
  }).locator("p").textContent())!.trim();
  expect(hash).toMatch(/^[a-f0-9]{64}$/);
  return {
    url: page.url(), id: id!, hash,
    header: await page.locator(".beleg__invoice").innerText(),
    lines: await page.locator(".beleg--document > .tablewrap table").innerText(),
  };
}

/** Reopens the same document and verifies both its content and current balance. */
async function expectDocument(page: Page, document: FrozenDocument, open: string) {
  await page.goto(document.url);
  expect(await frozenDocument(page)).toEqual(document);
  await expect(page.locator(".beleg__payinfo strong")).toHaveText(`Offen: ${open} €`);
}

test("mobile WebKit books two invoices, allocates payments, closes and settles without changing history", async ({ page }) => {
  test.setTimeout(150_000);
  await page.goto("/login");
  await page.getByRole("textbox", { name: "Benutzername" }).fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await submit(page, page.getByRole("button", { name: "Anmelden", exact: true }));
  await expect(page.locator(".appbar")).toBeVisible();

  // This journey runs alone on a fresh stack as well as after the desktop suite.
  // Only the immutable seeded price base is reused; no earlier invoice is needed.
  await page.goto("/years");
  await page.locator("#neues-jahr > summary").click();
  const createYear = page.locator('form[action="/years"]');
  const year = await createYear.locator('[name="year"]').inputValue();
  await createYear.locator('[name="base_id"]').selectOption("1");
  await submit(page, createYear.getByRole("button", { name: "Anlegen", exact: true }));
  const yearID = new URL(page.url()).searchParams.get("year")!;
  expect(yearID).toMatch(/^\d+$/);
  const neighborName = `Synthetic mobile billing ${year}`;

  await page.goto("/neighbors");
  await page.getByRole("link", { name: "Nachbar anlegen", exact: true }).click();
  const createNeighbor = page.locator('form[action="/neighbors/create"]');
  await createNeighbor.locator('[name="name"]').fill(neighborName);
  await submit(page, createNeighbor.getByRole("button", { name: "Anlegen", exact: true }));
  const neighbor = page.locator(".neighbor-row").filter({ has: page.getByText(neighborName, { exact: true }) });
  await expect(neighbor).toHaveCount(1);
  const neighborID = (await neighbor.getAttribute("id"))!.replace("neighbor-", "");
  expect(neighborID).toMatch(/^\d+$/);
  await neighbor.locator(":scope > details > summary").click();
  const editNeighbor = neighbor.locator(`form[action="/neighbors/${neighborID}/update"]`);
  await editNeighbor.getByText("Rechnungs- und Kontaktdaten", { exact: true }).click();
  await editNeighbor.locator('[name="address"]').fill("Testfeld 2\n4710 Testdorf");
  await submit(page, editNeighbor.getByRole("button", { name: "Speichern", exact: true }));
  const csrf = await page.locator('form[action="/neighbors/create"] [name="csrf_token"]').inputValue();
  const membership = await page.request.post("/years/add-neighbor", { form: {
    csrf_token: csrf, year_id: yearID, neighbor_id: neighborID,
  } });
  expect(membership.ok()).toBeTruthy();

  await page.goto("/admin/company");
  const company = page.locator('form[action="/admin/company"]');
  await company.locator('[name="name"]').fill("Synthetic mobile billing issuer");
  await company.locator('[name="address"]').fill("Testfeld 1\n4710 Testdorf");
  await company.locator('[name="tax_mode"]').selectOption("kleinunternehmer");
  await company.locator('[name="vat_rate"]').fill("0");
  await company.locator('[name="tax_note"]').fill("Synthetischer Kleinunternehmer-Test ohne Umsatzsteuerausweis.");
  await submit(page, company.getByRole("button", { name: "Speichern", exact: true }));

  const account = `/neighbors/${neighborID}?year=${yearID}`;
  const paymentsURL = `${account}&view=payments`;
  const book = async (label: string, amount: string) => {
    await page.goto(`${account}&view=booking`);
    const form = page.locator("[data-unified-booking]");
    await form.locator('[name="booking_direction"][value="out"]').check();
    await form.locator('[name="booking_kind"]').selectOption("quantity");
    await form.locator('[name="entry_date"]').fill(`${year}-05-01`);
    await form.locator('[name="unit"]').selectOption("Ballen");
    await form.locator('[name="quantity"]').fill("1");
    await form.locator('[name="unit_price"]').fill(amount);
    await form.locator('[name="task_label"]').fill(label);
    await submit(page, form.getByRole("button", { name: "Buchung speichern", exact: true }));
    await expect(page.locator(".bcard").filter({ hasText: label })).toHaveCount(1);
  };
  const issue = async (label: string, amount: string) => {
    await page.goto(`/neighbors/${neighborID}/invoice/confirm?year=${yearID}`);
    await expect(page.locator("main")).toContainText(label);
    await page.getByRole("button", { name: "Jetzt festschreiben", exact: true }).click();
    await confirm(page);
    await expect(page.locator(".beleg__payinfo strong")).toHaveText(`Offen: ${amount} €`);
    const document = await frozenDocument(page);
    await page.getByRole("button", { name: "Als versendet markieren", exact: true }).click();
    await confirm(page);
    return document;
  };
  const pay = async (invoiceID: string, amount: string, note: string) => {
    await page.goto(paymentsURL);
    const form = page.locator(`form[action="/neighbors/${neighborID}/payments"]`);
    await form.locator('[name="invoice_id"]').selectOption(invoiceID);
    await form.locator('[name="amount"]').fill(amount);
    await form.locator('[name="paid_on"]').fill(`${year}-05-02`);
    await form.locator('[name="method"]').selectOption("bar");
    await form.locator('[name="note"]').fill(note);
    await submit(page, form.getByRole("button", { name: "Zahlung erfassen", exact: true }));
    await expect(page.locator(".bcard").filter({ hasText: note })).toHaveCount(1);
  };

  await book("Synthetic mobile first work", "100");
  const first = await issue("Synthetic mobile first work", "100,00");
  await book("Synthetic mobile subsequent work", "40");
  const second = await issue("Synthetic mobile subsequent work", "40,00");
  expect(second.id).not.toBe(first.id);
  expect(second.lines).not.toContain("Synthetic mobile first work");
  await expectDocument(page, first, "100,00");

  await pay(second.id, "5", "Synthetic selected payment");
  await expectDocument(page, first, "100,00");
  await expectDocument(page, second, "35,00");
  await pay("", "7", "Synthetic FIFO payment");
  await expectDocument(page, first, "93,00");
  await expectDocument(page, second, "35,00");
  await page.goto(paymentsURL);
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("128,00 €");
  const paymentRows = page.locator(".bookcards > .bcard");
  await expect(paymentRows).toHaveCount(2);
  const originalPayments = await paymentRows.allTextContents();

  await page.goto(`/years/${yearID}/abschluss`);
  await page.getByRole("button", { name: new RegExp(`Jahr ${year} abschließen`) }).click();
  await confirm(page);
  await expect(page.locator("body")).toContainText("Abrechnungsjahr abgeschlossen.");
  await page.goto(`${account}&view=booking`);
  await expect(page.locator("main")).toContainText("neue Buchungen und Löschungen sind gesperrt.");
  await expect(page.getByRole("button", { name: "Buchung speichern", exact: true })).toHaveCount(0);

  await page.goto(paymentsURL);
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("128,00 €");
  expect(await paymentRows.allTextContents()).toEqual(originalPayments);
  const paymentForm = page.locator(`form[action="/neighbors/${neighborID}/payments"]`);
  await paymentForm.locator('[name="invoice_id"]').selectOption(second.id);
  await paymentForm.locator('[name="paid_on"]').fill(`${year}-05-03`);
  await paymentForm.locator('[name="method"]').selectOption("bar");
  await paymentForm.locator('[name="note"]').fill("Synthetic final settlement");
  const replay = await paymentForm.evaluate(form => Object.fromEntries(new FormData(form as HTMLFormElement).entries()) as Record<string, string>);
  await submit(page, page.getByRole("button", { name: "Gesamten Rest bezahlen", exact: true }));
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("0,00 €");
  await expect(page.getByRole("button", { name: "Gesamten Rest bezahlen", exact: true })).toHaveCount(0);
  await expect(paymentRows).toHaveCount(3);
  const savedPayments = await paymentRows.allTextContents();
  expect(savedPayments).toEqual(expect.arrayContaining(originalPayments));
  await expect(paymentRows.filter({ hasText: "Synthetic final settlement" }).locator(".bcard__cost")).toHaveText("−128,00 €");

  // Replay the captured browser submission to verify a stale double-submit is
  // harmless after closure. Only the fresh test account is addressed.
  expect((await page.request.post("/years/mark-paid", { form: replay })).ok()).toBeTruthy();
  await page.goto(paymentsURL);
  await expect(paymentRows).toHaveCount(3);
  expect(await paymentRows.allTextContents()).toEqual(savedPayments);
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("0,00 €");
  await expectDocument(page, first, "0,00");
  await expectDocument(page, second, "0,00");
});
