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
  await expect(details.locator("summary")).toHaveText("Zahlung verbuchen");
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
    frozenDocuments.push({ url, lines: await page.getByRole("table").innerText() });
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
    await expect(page.getByRole("table")).toHaveText(document.lines, { useInnerText: true });
    await expect(page.locator("#beleg")).toContainText("festgeschrieben");
  }
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
