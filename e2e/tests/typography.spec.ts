import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import path from "node:path";

const stylesheet = readFileSync(path.resolve(__dirname, "../../internal/web/static/css/app.css"), "utf8");
// Exercise the shipped cascade, including its tablet breakpoint and selectors
// that otherwise need backup files or a multi-year statistics fixture.
const labels = `
  <span class="status" data-label>In Bearbeitung</span>
  <span class="neighbor-account-card__label" data-label>Offen</span>
  <span class="paychip" data-label>Bezahlt</span>
  <label class="field"><span data-label>Bezeichnung</span><input class="input"></label>
  <input type="file" class="input" aria-label="Datei">
  <span class="kpi__label" data-label>Maschinenstunden</span>
  <span class="delta" data-label>+12 %</span>
  <span class="barpair__v" data-label>178,20 €</span>
  <span class="barpair__yr" data-label>2026</span>
  <span class="sparktip is-on" data-label>2026: 178,20 €</span>
  <span class="dchart__legend" data-label>Leistungen</span>
  <dl class="bkp__meta"><div><dt data-label>Wiederherstellungsprüfung</dt><dd>Heute</dd></div><div><dt data-label>Leistungsbuchungen</dt><dd>6</dd></div></dl>
  <span class="bkp__tlbl" data-label>Aufbewahrung</span>
  <span class="bkp__fm" data-label>12,3 MB</span>
  <div class="bkp__files--compact"><span class="bkp__fn" data-label>backup.sql</span><span class="bkp__fm" data-label>12,3 MB</span></div>
  <div class="bkp__seg"><button data-label>Wöchentlich</button></div>
  <span class="drawer__account-hint" data-label>Nur Lesezugriff</span>
  <div class="drawer__group-toggle"><small data-label>Einstellungen</small></div>
  <span class="basebar__label" data-label>Grundlage</span>
  <span class="bcard__date" data-label>15.10.2026</span>
  <span class="yearcard__meta" data-label>2 Buchungen</span>
  <span class="cmdk__ic" data-label>Jahr</span>
  <button class="btn btn--xs" data-label>Öffnen</button>
  <a class="tabbar__item" data-label>Nachbarn</a>
  <span class="beleg__inv-k" data-label>Von</span>`;

for (const theme of ["light", "dark"]) {
  test(`operational labels retain their screen floor at all breakpoints (${theme})`, async ({ page }) => {
    // All requests are served locally: this fixture never contacts the app or
    // writes a booking, invoice, backup or any other persistent record.
    await page.route("**/*", route => {
      const url = new URL(route.request().url());
      if (url.pathname === "/app.css") return route.fulfill({ contentType: "text/css", body: stylesheet });
      if (url.pathname === "/fixture") return route.fulfill({ contentType: "text/html", body:
        `<!doctype html><html lang="de" data-theme="${theme}"><head><link rel="stylesheet" href="/app.css"></head><body>${labels}</body></html>` });
      return route.fulfill({ status: 404 });
    });
    await page.goto("http://typography.test/fixture");
    for (const width of [320, 600, 768, 1023, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      const tooSmall = await page.locator("[data-label]").evaluateAll(nodes => nodes.flatMap(node => {
        const size = parseFloat(getComputedStyle(node).fontSize);
        return size < 11.99 ? [{ label: node.textContent, size }] : [];
      }));
      expect(tooSmall, `operational labels at ${width}px`).toEqual([]);
      const pickerSize = await page.locator('input[type="file"]').evaluate(node =>
        parseFloat(getComputedStyle(node, "::file-selector-button").fontSize));
      expect(pickerSize).toBeGreaterThanOrEqual(11.99);
      const overflowingLabels = await page.locator(".bkp__meta dt").evaluateAll(nodes =>
        nodes.filter(node => node.scrollWidth > node.clientWidth + 1).map(node => node.textContent));
      expect(overflowingLabels, `long metadata labels at ${width}px`).toEqual([]);
    }
    // The screen-only readability change must not alter frozen document print
    // geometry. This invoice label keeps the original .6rem print definition.
    await page.emulateMedia({ media: "print" });
    expect(await page.locator(".beleg__inv-k").evaluate(node =>
      parseFloat(getComputedStyle(node).fontSize))).toBeCloseTo(9.6, 1);
  });
}
