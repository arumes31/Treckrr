import { expect, test, type Download } from "@playwright/test";
import { readFileSync } from "node:fs";
import path from "node:path";

/** Reads a built static asset so the fixture runs the shipped script. */
const asset = (file: string) => readFileSync(path.resolve(__dirname, "../../internal/web/static", file));
// A synthetic one-pixel image stays inline; no screenshot or customer document is stored.
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aZ1sAAAAASUVORK5CYII=", "base64");
const cases = [
  { name: "plain PNG", contentType: "image/png", accepted: true },
  { name: "parameterized PNG", contentType: "image/png; charset=binary", accepted: true },
  { name: "mixed case and whitespace", contentType: " IMAGE/PNG ; charset=binary ", accepted: true },
  { name: "missing media type", contentType: "", accepted: false },
  { name: "different image format", contentType: "image/svg+xml", accepted: false },
  { name: "PNG prefix only", contentType: "image/png-invalid", accepted: false },
  { name: "HTML response", contentType: "text/html", accepted: false },
  { name: "failed PNG request", contentType: "image/png", status: 500, accepted: false },
  { name: "non-local image", contentType: "image/png", external: true, accepted: false },
];

for (const action of ["Bild", "Teilen"]) {
  for (const scenario of cases) {
    test(`${action} export: ${scenario.name}`, async ({ page }) => {
      const errors: string[] = [];
      const downloads: Download[] = [];
      let imageFetches = 0;
      page.on("pageerror", error => errors.push(error.message));
      page.on("download", download => downloads.push(download));
      const source = scenario.external ? "http://external-image.test/logo.png" : "/logo.png";
      await page.route("**/*", route => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.pathname === "/fixture") return route.fulfill({ contentType: "text/html", body:
          `<!doctype html><html lang="de"><head><meta charset="utf-8"></head><body><main>
          <article id="beleg" data-beleg-name="synthetic-invoice"><h1>Rechnung</h1>
          <img src="${source}" width="20" height="20" alt="Testlogo"><p>Offen: 178,20 €</p></article>
          <button data-beleg-image>Bild</button><button data-beleg-share>Teilen</button>
          </main><script src="/static/js/app.js"></script></body></html>` });
        if (url.pathname === "/static/js/app.js") return route.fulfill({ contentType: "application/javascript", body: asset("js/app.js") });
        if (/^\/static\/fonts\/[a-z0-9-]+\.woff2$/.test(url.pathname)) {
          return route.fulfill({ contentType: "font/woff2", body: asset(url.pathname.slice(8)) });
        }
        if (url.pathname === "/logo.png") {
          // The displayed image loads normally; vary the response fetched by the exporter.
          if (request.resourceType() !== "fetch") return route.fulfill({ contentType: "image/png", body: png });
          imageFetches++;
          return route.fulfill({ status: scenario.status || 200,
            headers: { "content-type": scenario.contentType }, body: png });
        }
        return route.fulfill({ status: 404 });
      });
      await page.goto("http://image-export.test/fixture");
      const before = await page.locator("#beleg").innerHTML();
      const button = page.getByRole("button", { name: action, exact: true });
      await button.click();
      if (scenario.accepted) {
        await expect(page.getByRole("status")).toHaveText(action === "Bild" ? "Beleg als Bild gespeichert" : "Teilen hier nicht möglich – Beleg gespeichert");
        await expect.poll(() => downloads.length).toBe(1);
        const download = downloads[0];
        expect(await download.failure()).toBeNull();
        const stream = await download.createReadStream();
        const chunks: Buffer[] = [];
        for await (const chunk of stream!) chunks.push(Buffer.from(chunk));
        const image = Buffer.concat(chunks);
        expect(image.subarray(0, 8).equals(png.subarray(0, 8))).toBe(true);
        expect(image.length).toBeGreaterThan(100);
        expect(download.suggestedFilename()).toBe(action === "Bild" ? "beleg.png" : "synthetic-invoice.png");
      } else {
        await expect(page.getByRole("status")).toHaveText("Bild-Export hier nicht möglich – nutze Drucken/PDF");
        expect(downloads).toEqual([]);
      }
      await expect(button).toBeEnabled();
      expect(imageFetches).toBe(scenario.external ? 0 : 1);
      expect(await page.locator("#beleg").innerHTML()).toBe(before);
      expect(errors).toEqual([]);
    });
  }
}
