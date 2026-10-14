import { test, expect } from "@playwright/test";

// Session scripts for the flows this app is actually used for (Ausbaukarte 96).
// booking.spec and invoice.spec cover capture and invoicing; this file walks the
// paths added later — money in, the bookings list with its filters and bulk
// actions, and closing the year — because those are the ones where a regression
// costs an operator real work rather than a re-click.
//
// The CI job seeds base/year/neighbor 1/1/1, master data and one booking.

const USER = process.env.E2E_ADMIN_USER || "admin";
const PASS = process.env.E2E_ADMIN_PASS || "e2e-admin-password-123";

async function login(page) {
  await page.goto("/login");
  await page.locator('input[name="username"]').fill(USER);
  await page.locator('input[name="password"]').fill(PASS);
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();
}

// Actions guarded by data-confirm open the custom <dialog>; its OK submits the
// pending form. #confirmModal lives in the layout on every page, so waiting for
// "attached" would race showModal() — wait for the button to be VISIBLE.
async function confirmModal(page, waitForURL?: RegExp) {
  const okBtn = page.locator("#confirmModal [data-modal-ok]");
  await okBtn.waitFor({ state: "visible", timeout: 4000 });
  if (waitForURL) {
    await Promise.all([
      page.waitForURL(waitForURL, { timeout: 10000 }),
      okBtn.click({ force: true }),
    ]);
  } else {
    await Promise.all([
      page.waitForNavigation({ waitUntil: "load", timeout: 10000 }),
      okBtn.click({ force: true }),
    ]);
  }
}

test("record a payment and see it in the cross-year history", async ({ page }) => {
  await login(page);
  await page.goto("/neighbors/1?year=1&view=payments");

  const details = page.locator("#zahlung-erfassen");
  await expect(details).toHaveAttribute("open", "");
  await expect(details.locator(":scope > summary")).toHaveText("Zahlung verbuchen");
  await details.locator('input[name="amount"]').fill("25");
  await details.locator('select[name="method"]').selectOption("bar");
  await details.locator('input[name="note"]').fill("E2E Teilzahlung");
  await details.getByRole("button", { name: "Zahlung erfassen" }).click();

  // The row shows method and note where the payment was booked …
  await expect(page.locator("body")).toContainText("E2E Teilzahlung");
  // … and the cross-year history lists it too (Ausbaukarte 79).
  await page.goto("/neighbors/1/overview");
  await expect(page.locator("body")).toContainText("25,00");
  await expect(page.locator("body")).toContainText("E2E Teilzahlung");
});

test("bookings list filters, and bulk storno leaves a reason", async ({ page }, testInfo) => {
  test.setTimeout(60_000);
  await login(page);
  await page.goto("/buchungen?year=1");
  await expect(page.locator("body")).toContainText("Buchung(en)");

  // A task filter that matches nothing must say so, not show everything.
  await page.goto("/buchungen?year=1&task=gibtesnicht");
  await expect(page.locator("body")).toContainText("0 Buchung(en)");

  // Preserve existing invoices. A bulk booking test must not cancel documents
  // created by another scenario merely to unlock their historical sources.
  await page.goto("/neighbors/1/beleg?year=1");
  const documentURLs = [...new Set(await page.locator('a[href*="/beleg?"][href*="invoice_id="]')
    .evaluateAll((links) => links.map((link) => link.getAttribute("href")!)))];
  const frozenDocuments: { url: string; lines: string }[] = [];
  for (const url of documentURLs) {
    await page.goto(url);
    frozenDocuments.push({ url, lines: await page.locator(".beleg--document > .tablewrap table").innerText() });
  }

  // Two fresh, mutable bookings isolate this test from previous financial work
  // and from any partially completed retry, without changing an old invoice.
  const task = `E2E Bulk ${testInfo.retry} ${Date.now()}`;
  const reason = `${task} Sammelstorno`;
  for (const suffix of ["A", "B"]) {
    await page.goto("/neighbors/1?year=1&view=booking");
    await page.locator("[data-booking-kind]").selectOption("quantity");
    await page.locator("[data-unit]").selectOption("Ballen");
    await page.locator('input[name="quantity"]').fill("1");
    await page.locator('input[name="unit_price"]').fill("2.50");
    await page.locator('input[name="task_label"]').fill(`${task} ${suffix}`);
    await page.getByRole("button", { name: "Buchung speichern", exact: true }).click();
    await expect(page.getByText(`${task} ${suffix}`, { exact: true }).first()).toBeVisible();
    await page.waitForLoadState("load");
  }
  const filteredURL = `/buchungen?year=1&task=${encodeURIComponent(task)}`;
  await page.goto(filteredURL);
  const boxes = page.locator('input[name="booking_id"]');
  await expect(boxes).toHaveCount(2);
  const entryIDs = await boxes.evaluateAll((inputs) => inputs.map((input) => (input as HTMLInputElement).value.split(":")[1]));
  for (const box of await boxes.all()) await box.check();
  await page.locator('input[name="reason"]').fill(reason);
  await page.getByRole("button", { name: "Stornieren", exact: true }).click();
  await confirmModal(page, /\/buchungen/);

  // The count in the flash must be the selection, not zero — a bulk action that
  // silently does nothing is the failure mode worth pinning.
  // Exact text avoids falsely accepting "0 von 2 Buchung(en) storniert".
  await expect(page.getByText("2 Buchung(en) storniert.", { exact: true })).toBeVisible();
  await page.goto(`${filteredURL}&voided=only`);
  await expect(page.locator('tbody tr.dtable__row--bad')).toHaveCount(2);
  await page.goto("/neighbors/1?year=1&view=bookings");
  for (const id of entryIDs) {
    await expect(page.locator(`#entry-${id}`)).toHaveClass(/bcard--void/);
    await expect(page.locator(`#entry-${id}`)).toContainText(`Storno: ${reason}`);
  }
  for (const document of frozenDocuments) {
    await page.goto(document.url);
    await expect(page.locator(".beleg--document > .tablewrap table")).toHaveText(document.lines, { useInnerText: true });
    await expect(page.locator("#beleg")).toContainText("festgeschrieben");
  }
});

test("year closing keeps additional account residual visible after the invoice is paid", async ({ page }) => {
  test.setTimeout(90_000);
  await login(page);

  // A fresh account-year keeps this scenario independent of other specs and
  // retries. The disposable seed supplies base 1 and neighbor 1 with an address.
  await page.goto("/years");
  const create = page.locator('form[action="/years"]');
  const year = await create.locator('[name="year"]').inputValue();
  const csrf = await create.locator('[name="csrf_token"]').inputValue();
  const created = await page.request.post("/years", { form: {
    csrf_token: csrf, year, base_id: "1", label: `Account residual E2E ${year}`,
  } });
  expect(created.ok()).toBeTruthy();
  const yearID = new URL(created.url()).searchParams.get("year")!;
  expect(yearID).toBeTruthy();
  expect((await page.request.post("/years/add-neighbor", { form: {
    csrf_token: csrf, year_id: yearID, neighbor_id: "1",
  } })).ok()).toBeTruthy();
  const paymentsURL = `/neighbors/1?year=${yearID}&view=payments`;
  const closingURL = `/years/${yearID}/abschluss`;

  // Supply the invoice prerequisites even when this single test runs alone.
  await page.goto("/admin/company");
  const company = page.locator('form[action="/admin/company"]');
  await company.locator('[name="name"]').fill("Hof Bergmann");
  await company.locator('[name="address"]').fill("Feldweg 3\n4780 Schärding");
  await company.locator('[name="tax_mode"]').selectOption("kleinunternehmer");
  await company.locator('[name="vat_rate"]').fill("0");
  await company.locator('[name="tax_note"]').fill("Synthetischer Kleinunternehmer-Test ohne Umsatzsteuerausweis.");
  await Promise.all([
    page.waitForNavigation({ waitUntil: "load" }),
    company.getByRole("button", { name: "Speichern", exact: true }).click(),
  ]);

  const book = async (kind: "quantity" | "fixed", amount: string, label: string) => {
    await page.goto(`/neighbors/1?year=${yearID}&view=booking`);
    const form = page.locator("[data-unified-booking]");
    await form.locator('[name="booking_direction"][value="out"]').check();
    await form.locator('[name="booking_kind"]').selectOption(kind);
    await form.locator('[name="entry_date"]').fill(`${year}-05-01`);
    await form.locator('[name="task_label"]').fill(label);
    if (kind === "quantity") {
      await form.locator('[name="unit"]').selectOption("Ballen");
      await form.locator('[name="quantity"]').fill("1");
      await form.locator('[name="unit_price"]').fill(amount);
    } else {
      await form.locator('[name="amount"]').fill(amount);
    }
    await form.getByRole("button", { name: "Buchung speichern", exact: true }).click();
    await expect(page.locator(".bcard").filter({ hasText: label })).toHaveCount(1);
    await page.waitForLoadState("load");
  };
  await book("quantity", "100", `E2E issued work ${year}`);
  await page.goto(`/neighbors/1/invoice/confirm?year=${yearID}`);
  await page.waitForLoadState("load");
  await page.getByRole("button", { name: "Jetzt festschreiben", exact: true }).click();
  await confirmModal(page, /\/neighbors\/1\/beleg\?.*invoice_id=/);
  const invoiceURL = page.url();
  const frozenLines = await page.locator(".beleg--document > .tablewrap table").innerText();
  await expect(page.locator(".beleg__payinfo strong")).toHaveText("Offen: 100,00 €");
  await page.locator('form[action*="mark-sent"] button').click();
  await confirmModal(page);

  // This positive account posting is deliberately not an invoice source.
  await book("fixed", "20", `E2E additional account cost ${year}`);
  await page.goto(paymentsURL);
  const payment = page.locator('form[action="/neighbors/1/payments"]');
  await payment.locator('[name="invoice_id"]').selectOption("");
  await payment.locator('[name="amount"]').fill("110");
  await payment.getByRole("button", { name: "Zahlung erfassen", exact: true }).click();
  await page.waitForLoadState("load");
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("10,00 €");
  await page.goto(invoiceURL);
  await expect(page.locator(".beleg__payinfo strong")).toHaveText("Offen: 0,00 €");
  await expect(page.locator(".beleg--document > .tablewrap table")).toHaveText(frozenLines, { useInnerText: true });

  await page.goto(closingURL);
  const residual = page.locator(".closing-check").filter({ has: page.getByText("Zusätzliche offene Kontoposten", { exact: true }) });
  await expect(residual).toBeVisible();
  await expect(residual).toContainText("Summe 10,00 €");
  await expect(residual.locator(".status")).toHaveText("1 offen");
  await expect(page.locator(".closing-check").filter({ has: page.getByText("Offene Beträge", { exact: true }) })).toHaveCount(0);
  const review = residual.getByRole("link", { name: "E2E Nachbar prüfen", exact: true });
  await expect(review).toHaveAttribute("href", paymentsURL);
  await review.click();
  await expect(page).toHaveURL(new RegExp(`/neighbors/1\\?year=${yearID}&view=payments$`));
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("10,00 €");

  // Closing is allowed with warnings; settling that residual must still work
  // afterwards and must not change the already paid, frozen invoice.
  await page.goto(closingURL);
  await page.getByRole("button", { name: new RegExp(`Jahr ${year} abschließen`) }).click();
  await confirmModal(page, /\/years$/);
  await expect(page.locator("body")).toContainText("Abrechnungsjahr abgeschlossen.");
  await page.goto(paymentsURL);
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("10,00 €");
  await page.getByRole("button", { name: "Gesamten Rest bezahlen", exact: true }).click();
  await page.waitForLoadState("load");
  await expect(page.locator("header.summary-card .summary-card__value")).toHaveText("0,00 €");
  await expect(page.locator("header.summary-card .summary-card__label").first()).toContainText("Ausgeglichen");
  await page.goto(closingURL);
  await expect(page.getByText("Dieses Jahr ist bereits abgeschlossen.", { exact: true })).toBeVisible();
  await expect(residual).toHaveCount(0);
  await page.goto(invoiceURL);
  await expect(page.locator(".beleg__payinfo strong")).toHaveText("Offen: 0,00 €");
  await expect(page.locator(".beleg--document > .tablewrap table")).toHaveText(frozenLines, { useInnerText: true });
});

test("year closing review lists what is open, and closing locks documents", async ({ page }) => {
  await login(page);
  await page.goto("/years/1/abschluss");
  await expect(page.locator("body")).toContainText("Jahresabschluss");
  await expect(page.locator("body")).toContainText("Buchungen ohne festgeschriebene Rechnung");

  await page.getByRole("button", { name: /Jahr \d{4} abschließen/ }).click();
  await confirmModal(page, /\/years/);
  await expect(page.locator("body")).toContainText("Abgeschlossen");

  // A closed year refuses a new document and says why …
  await page.goto("/neighbors/1/beleg?year=1");
  await expect(page.locator("body")).not.toContainText("Rechnung ausstellen & festschreiben");

  // … and reopening needs a reason, which the form asks for.
  await page.goto("/years");
  await expect(page.locator("body")).toContainText(/wieder öffnen/i);
});
