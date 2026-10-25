import { expect, settled, test, workerApi } from "../support/env";
import { ensureSample, mainPages, type Sample } from "../support/sample";

const WIDTHS = [320, 768, 1440] as const;
let sample: Sample;

test.beforeAll(async ({ playwright }) => {
  const { api, dispose } = await workerApi(playwright);
  sample = await ensureSample(api);
  await dispose();
});

for (const width of WIDTHS) {
  test(`responsive ${width}px : pas de défilement horizontal, navigation principale accessible`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    for (const p of mainPages(sample)) {
      await test.step(p.name, async () => {
        await page.goto(p.path);
        await expect(page.getByRole("heading", { level: 1 })).toHaveText(p.heading);
        await settled(page);
        const overflow = await page.evaluate(() => ({
          scroll: document.documentElement.scrollWidth,
          client: document.documentElement.clientWidth,
        }));
        expect(overflow.scroll, `${p.path} à ${width}px : scrollWidth ${overflow.scroll} > clientWidth ${overflow.client}`).toBeLessThanOrEqual(overflow.client);

        const nav = page.getByRole("navigation", { name: "Navigation principale" });
        if (width < 1024) {
          // Navigation repliée : bouton de menu puis tiroir modal refermable au clavier.
          await expect(nav).toBeHidden();
          const open = page.getByRole("button", { name: "Ouvrir le menu" });
          await expect(open).toBeVisible();
          await open.click();
          const drawer = page.getByRole("dialog", { name: "Menu" });
          await expect(drawer.getByRole("navigation", { name: "Navigation principale" }).getByRole("link", { name: "Features" })).toBeVisible();
          await page.keyboard.press("Escape");
          await expect(drawer).toBeHidden();
        } else {
          await expect(nav.getByRole("link", { name: "Features" })).toBeVisible();
          await expect(nav.getByRole("link", { name: "Audit" })).toBeVisible();
        }
      });
    }
  });
}
