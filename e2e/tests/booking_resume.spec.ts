import { expect, test, type Page } from "@playwright/test";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Benutzername").fill(process.env.E2E_ADMIN_USER || "admin");
  await page.locator('input[name="password"]').fill(process.env.E2E_ADMIN_PASS || "e2e-admin-password-123");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(page.locator(".appbar")).toBeVisible();
  await page.goto("/neighbors/1?year=1&view=booking");
}

async function fillQuantity(page: Page, description: string) {
  const form = page.locator("[data-unified-booking]");
  await form.locator('[name="booking_kind"]').selectOption("quantity");
  await form.locator('[name="entry_date"]').fill("2025-05-03");
  await form.locator('[name="unit"]').selectOption("Ballen");
  await form.locator('[name="quantity"]').fill("3");
  await form.locator('[name="unit_price"]').fill("7");
  await form.locator('[name="task_label"]').fill(description);
  return form;
}

for (const version of [undefined, "2"]) {
  test(`pre-upgrade flat-rate draft ${version || "without version"} remains a visible account posting`, async ({ page }) => {
    await login(page);
    const form = page.locator("[data-unified-booking]");
    await form.locator('[name="booking_kind"]').selectOption("fixed");
    await form.locator('[name="amount"]').fill("45");
    const task = `Synthetic legacy material draft ${version || "missing version"}`;
    await form.locator('[name="task_label"]').fill(task);
    const user = await page.locator('meta[name="user-id"]').getAttribute("content");
    const key = `treckrr:booking-draft:v1:${user}:1:1`;
    await expect.poll(() => page.evaluate(key => {
      const draft = JSON.parse(localStorage.getItem(key) || "null");
      return draft?.fields?.find(field => field.name === "task_label")?.value;
    }, key)).toBe(task);
    await page.evaluate(({ key, version }) => {
      const draft = JSON.parse(localStorage.getItem(key)!);
      if (version) draft.formVersion = version;
      else delete draft.formVersion;
      localStorage.setItem(key, JSON.stringify(draft));
    }, { key, version });
    await page.reload();
    await page.locator("[data-booking-draft-restore]").click();
    await expect(form.locator('[name="booking_form_version"]')).toHaveValue("2");
    await expect(form.locator('[name="amount"]')).toHaveValue("45");
    await expect(form.locator("[data-booking-direction-note]")).toContainText("bleibt außerhalb der Rechnung");
    const restored = await page.evaluate(key => JSON.parse(localStorage.getItem(key)!), key);
    expect(restored.formVersion).toBe("2");
    await form.getByRole("button", { name: "Buchung speichern", exact: true }).click();
    const card = page.locator(".bcard").filter({ hasText: task });
    await expect(card).toHaveCount(1);
    await expect(card.locator('a[href^="/ledger/"][href$="/edit"]')).toHaveCount(1);
    await expect(card.locator(".bcard__cost")).toContainText("45,00");
    expect(await page.evaluate(key => localStorage.getItem(key), key)).toBeNull();
    await page.goto("/neighbors/1?year=1&view=booking");
    await expect(form.locator('[name="booking_form_version"]')).toHaveValue("3");
  });
}

for (const kind of ["equipment", "quantity"]) {
  test(`legacy unscoped own ${kind} defaults cannot restore or create an initial draft`, async ({ page }) => {
    const legacy = JSON.stringify({ booking_direction: "out", booking_kind: kind, unit: "Ballen", task_label: "Fremder alter Standard", mode: "free" });
    await page.addInitScript((value) => localStorage.setItem("treckrr:entry-defaults:1", value), legacy);
    await login(page);
    const form = page.locator("[data-unified-booking]");
    const user = await page.locator('meta[name="user-id"]').getAttribute("content");
    await expect(form.locator('[name="booking_kind"]')).toHaveValue("equipment");
    await expect(form.locator('[name="task_label"]')).not.toHaveValue("Fremder alter Standard");
    expect(await page.evaluate((key) => localStorage.getItem(key), `treckrr:booking-draft:v1:${user}:1:1`)).toBeNull();
    await expect(page.locator("[data-booking-draft-status]")).not.toContainText("Ungespeicherter Entwurf");
    await form.locator('[name="booking_kind"]').selectOption("quantity");
    await form.locator('[name="task_label"]').fill("Neuer benutzerbezogener Standard");
    expect(await page.evaluate(() => localStorage.getItem("treckrr:entry-defaults:1"))).toBe(legacy);
  });
}

test("unsaved booking draft is offered explicitly, restored and discarded without a booking", async ({ page }) => {
  await login(page);
  let posted = 0;
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/entries") posted++;
  });
  const form = await fillQuantity(page, "E2E ungespeicherter Entwurf");
  await form.locator('details:has([name="note"]) > summary').click();
  await form.locator('[name="note"]').fill("Nur lokal, kein Buchungsauftrag");
  await expect(page.locator("[data-booking-draft-status]")).toContainText("Noch keine Buchung");
  const user = await page.locator('meta[name="user-id"]').getAttribute("content");
  const key = `treckrr:booking-draft:v1:${user}:1:1`;
  const saved = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "null"), key);
  expect(saved.version).toBe(1);
  expect(saved.formVersion).toBe("3");
  expect(saved.fields.some((field) => field.name === "csrf_token")).toBe(false);

  await page.reload();
  await expect(page.locator("[data-booking-draft-offer]")).toBeVisible();
  await expect(form.locator('[name="task_label"]')).not.toHaveValue("E2E ungespeicherter Entwurf");
  await page.locator("[data-booking-draft-restore]").click();
  await expect(form.locator('[name="booking_kind"]')).toHaveValue("quantity");
  await expect(form.locator('[name="quantity"]')).toHaveValue("3");
  await expect(form.locator('[name="note"]')).toHaveValue("Nur lokal, kein Buchungsauftrag");
  await expect(form.locator('[name="task_label"]')).toHaveValue("E2E ungespeicherter Entwurf");
  await page.reload();
  await page.locator("[data-booking-draft-discard]").click();
  await expect(page.locator("[data-booking-draft-offer]")).toBeHidden();
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBeNull();

  // A draft for another user must not become an offer for the current account.
  await page.evaluate(({ key, saved }) => localStorage.setItem(key, JSON.stringify(saved)), { key: "treckrr:booking-draft:v1:999999:1:1", saved });
  await page.reload();
  await expect(page.locator("[data-booking-draft-offer]")).toBeHidden();
  expect(posted).toBe(0);
});

test("another tab's newer draft is retained during edits and confirmed-save cleanup", async ({ page }) => {
  await login(page);
  const form = await fillQuantity(page, "Entwurf dieses Tabs");
  const user = await page.locator('meta[name="user-id"]').getAttribute("content");
  const key = `treckrr:booking-draft:v1:${user}:1:1`;
  const other = await page.evaluate((key) => {
    const draft = JSON.parse(localStorage.getItem(key) || "null");
    const label = draft.fields.find((field) => field.name === "task_label");
    label.value = "Neuerer Entwurf eines anderen Tabs";
    const encoded = JSON.stringify(draft);
    localStorage.setItem(key, encoded);
    return encoded;
  }, key);

  await form.locator('[name="task_label"]').fill("Meine weitere Eingabe");
  await expect(page.locator("[data-booking-draft-status]")).toContainText("Ein anderer Tab");
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(other);
  expect(await page.evaluate(() => dispatchEvent(new Event("beforeunload", { cancelable: true })))).toBe(false);

  // Exercise the cleanup hook without submitting any synthetic booking: its
  // confirmation is allowed to clear only this form's own stored snapshot.
  await form.evaluate((node) => node.dispatchEvent(new CustomEvent("treckrr:booking-saved")));
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(other);
  expect(await page.evaluate(() => dispatchEvent(new Event("beforeunload", { cancelable: true })))).toBe(true);
});

for (const direction of ["out", "in"]) {
test(`save and next confirms ${direction}, retains context and clears booked amounts and draft`, async ({ page }) => {
  await login(page);
  // An independent year avoids a successful first attempt leaving a duplicate
  // confirmation behind if a later browser assertion needs a retry.
  await page.goto("/years");
  const create = page.locator('form[action="/years"]');
  const year = await create.locator('[name="year"]').inputValue();
  const csrf = await create.locator('[name="csrf_token"]').inputValue();
  const created = await page.request.post("/years", { form: { csrf_token: csrf, year, base_id: "1", label: `Next-booking E2E ${year}` } });
  expect(created.ok()).toBeTruthy();
  const yearID = new URL(created.url()).searchParams.get("year")!;
  expect(yearID).toBeTruthy();
  const assigned = await page.request.post("/years/add-neighbor", { form: { csrf_token: csrf, year_id: yearID, neighbor_id: "1" } });
  expect(assigned.ok()).toBeTruthy();
  await page.goto(`/neighbors/1?year=${yearID}&view=booking`);
  await page.locator(`[name="booking_direction"][value="${direction}"]`).check();
  const form = await fillQuantity(page, "E2E Speichern und nächste Buchung");
  await form.locator('[name="entry_date"]').fill(`${year}-05-03`);
  await form.locator('details:has([name="note"]) > summary').click();
  await form.locator('[name="note"]').fill("Nur für die gespeicherte Buchung");
  const firstKey = await form.locator('[name="idempotency_key"]').inputValue();
  const user = await page.locator('meta[name="user-id"]').getAttribute("content");
  let confirmed = 0;
  await page.exposeFunction("captureConfirmedBooking", () => { confirmed++; });
  await form.evaluate((node) => node.addEventListener("treckrr:booking-saved", () => (window as any).captureConfirmedBooking()));
  const response = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/entries");
  await page.locator("[data-booking-next]").click();
  // The app emits this event only after parsing saved=true. Capturing it before
  // navigation avoids reading a discarded CDP response body after redirect.
  expect((await response).status()).toBe(200);
  expect((await response).headers()["content-type"]).toContain("application/json");
  await expect.poll(() => confirmed).toBe(1);
  await expect.poll(() => new URL(page.url()).searchParams.get("next_kind")).toBe("quantity");
  expect(new URL(page.url()).searchParams.get("year")).toBe(yearID);
  expect(new URL(page.url()).searchParams.get("view")).toBe("booking");
  expect(new URL(page.url()).searchParams.get("next_direction")).toBe(direction);
  expect(new URL(page.url()).searchParams.get("next_date")).toBe(`${year}-05-03`);
  await expect(form.locator('[name="idempotency_key"]')).not.toHaveValue(firstKey);
  await page.waitForLoadState("load");
  await expect(page.locator("[data-booking-next]")).toBeEnabled();
  await expect(form.locator('[name="booking_kind"]')).toHaveValue("quantity");
  await expect(form.locator(`[name="booking_direction"][value="${direction}"]`)).toBeChecked();
  await expect(form.locator('[name="entry_date"]')).toHaveValue(`${year}-05-03`);
  await expect(form.locator('[name="quantity"]')).toHaveValue("");
  await expect(form.locator('[name="unit_price"]')).toHaveValue("");
  await expect(form.locator('[name="note"]')).toHaveValue("");
  expect(await page.evaluate((key) => localStorage.getItem(key), `treckrr:booking-draft:v1:${user}:${yearID}:1`)).toBeNull();
  await expect(page.locator("[data-booking-draft-status]")).not.toContainText("Ungespeicherter Entwurf");
  await page.goto(`/neighbors/1?year=${yearID}&view=bookings`);
  await expect(page.getByText("E2E Speichern und nächste Buchung").first()).toBeVisible();
});
}

for (const next of [false, true]) {
test(`native ${next ? "save and next" : "booking"} without IndexedDB neither creates nor replays a local draft`, async ({ page }) => {
  const direction = next ? "out" : "in";
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.addInitScript(() => { delete (window as any).indexedDB; });
  await login(page);
  expect(await page.evaluate(() => "indexedDB" in window)).toBe(false);
  await page.goto("/years");
  const create = page.locator('form[action="/years"]');
  const year = await create.locator('[name="year"]').inputValue();
  const csrf = await create.locator('[name="csrf_token"]').inputValue();
  const created = await page.request.post("/years", { form: { csrf_token: csrf, year, base_id: "1", label: `Native booking E2E ${year}` } });
  expect(created.ok()).toBeTruthy();
  const yearID = new URL(created.url()).searchParams.get("year")!;
  expect(yearID).toBeTruthy();
  expect((await page.request.post("/years/add-neighbor", { form: { csrf_token: csrf, year_id: yearID, neighbor_id: "1" } })).ok()).toBeTruthy();
  await page.goto(`/neighbors/1?year=${yearID}&view=booking`);
  const user = await page.locator('meta[name="user-id"]').getAttribute("content");
  const key = `treckrr:booking-draft:v1:${user}:${yearID}:1`;
  const form = await fillQuantity(page, "Native Eingabe ohne Entwurf");
  await form.locator('[name="entry_date"]').fill(`${year}-05-03`);
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBeNull();
  await expect(page.locator("[data-booking-draft-status]")).not.toContainText("Ungespeicherter Entwurf");

  // Preserve a draft left by a capable browser, but never offer to replay it in
  // the native path that cannot acknowledge and clear a successfully saved draft.
  const previous = JSON.stringify({ version: 1, fields: [{ name: "task_label", value: "Nicht erneut buchen", checked: false }], people: 0 });
  await page.evaluate(({ key, previous }) => localStorage.setItem(key, previous), { key, previous });
  await page.reload();
  await expect(page.locator("[data-booking-draft-offer]")).toBeHidden();
  await expect(form.locator('[name="task_label"]')).not.toHaveValue("Nicht erneut buchen");
  await form.locator(`[name="booking_direction"][value="${direction}"]`).check();
  await fillQuantity(page, "E2E native Buchung ohne IndexedDB");
  await form.locator('[name="entry_date"]').fill(`${year}-05-03`);
  const response = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/entries");
  const precheck = next ? page.waitForResponse((response) => new URL(response.url()).pathname === "/api/entries/precheck") : null;
  if (next) await page.locator("[data-booking-next]").click();
  else await page.getByRole("button", { name: "Buchung speichern", exact: true }).click();
  // Outgoing quantity runs the asynchronous precheck and requestSubmit() loses
  // the original submitter, so the hidden continuation intent must survive it.
  if (precheck) expect((await precheck).status()).toBe(200);
  expect((await response).status()).toBe(303);
  await expect(page).toHaveURL((url) => url.searchParams.get("view") === (next ? "booking" : "bookings") &&
    (!next || url.searchParams.get("next_kind") === "quantity"));
  if (next) {
    expect(new URL(page.url()).searchParams.get("next_kind")).toBe("quantity");
    expect(new URL(page.url()).searchParams.get("next_direction")).toBe(direction);
    expect(new URL(page.url()).searchParams.get("next_date")).toBe(`${year}-05-03`);
    await expect(form.locator('[name="booking_kind"]')).toHaveValue("quantity");
    await expect(form.locator(`[name="booking_direction"][value="${direction}"]`)).toBeChecked();
    await expect(form.locator('[name="entry_date"]')).toHaveValue(`${year}-05-03`);
    await expect(form.locator('[name="quantity"]')).toHaveValue("");
    await expect(form.locator('[name="unit_price"]')).toHaveValue("");
    await page.goto(`/neighbors/1?year=${yearID}&view=bookings`);
  }
  await expect(page.getByText("E2E native Buchung ohne IndexedDB").first()).toBeVisible();
  expect(await page.evaluate((key) => localStorage.getItem(key), key)).toBe(previous);
  await page.goto(`/neighbors/1?year=${yearID}&view=booking`);
  await expect(page.locator("[data-booking-draft-offer]")).toBeHidden();
  await expect(form.locator('[name="task_label"]')).not.toHaveValue("Nicht erneut buchen");
  expect(pageErrors, "native booking must not raise browser exceptions").toEqual([]);
});
}
