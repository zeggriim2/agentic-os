import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { expect, settled, test, workerApi } from "../support/env";
import { ensureSample, mainPages, type Sample } from "../support/sample";

let sample: Sample;

test.beforeAll(async ({ playwright }) => {
  const { api, dispose } = await workerApi(playwright);
  sample = await ensureSample(api);
  await dispose();
});

const BLOCKING = new Set(["serious", "critical"]);

/** Analyse axe (WCAG 2.1 A/AA + bonnes pratiques) ; échoue sur toute violation serious/critical. */
async function audit(page: Page, label: string) {
  const res = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "best-practice"]).analyze();
  const blocking = res.violations.filter((v) => BLOCKING.has(v.impact ?? ""));
  const report = blocking
    .map((v) => `- [${v.impact}] ${v.id} : ${v.help}\n${v.nodes.slice(0, 5).map((n) => `    ${n.target.join(" ")} — ${(n.failureSummary ?? "").split("\n").join(" ")}`).join("\n")}`)
    .join("\n");
  expect(blocking, `${label} : violations axe bloquantes\n${report}`).toEqual([]);
}

for (const theme of ["dark", "light"] as const) {
  test(`axe (thème ${theme}) : aucune violation serious/critical sur les pages principales`, async ({ page }) => {
    await page.addInitScript((t) => window.localStorage.setItem("aos-theme", t), theme);
    for (const p of mainPages(sample)) {
      await test.step(p.name, async () => {
        await page.goto(p.path);
        await expect(page.getByRole("heading", { level: 1 })).toHaveText(p.heading);
        await settled(page);
        await audit(page, `${p.path} (${theme})`);
      });
    }
  });
}

test("axe : menu mobile ouvert (320 px)", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 800 });
  await page.goto("/");
  await settled(page);
  await page.getByRole("button", { name: "Ouvrir le menu" }).click();
  await expect(page.getByRole("dialog", { name: "Menu" })).toBeVisible();
  await audit(page, "menu mobile");
});

test.describe("sans session", () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test("axe : page de connexion (avec erreur de saisie affichée)", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByRole("heading", { level: 1, name: "Agentic OS" })).toBeVisible();
    await audit(page, "/login");
    // Erreurs de validation côté client (aucun appel au serveur, la connexion est limitée en débit).
    await page.getByRole("button", { name: "Se connecter" }).click();
    await expect(page.getByText("L'identifiant est requis.")).toBeVisible();
    await audit(page, "/login (erreurs)");
  });
});
