import { expect, type Page } from "@playwright/test";

/** Badge d'état principal d'une page de détail (premier badge de l'en-tête). */
export function headerState(page: Page) {
  return page.locator("main header [data-state]").first();
}

/** Attend un état (code + libellé UI de web/src/lib/states.ts) dans l'en-tête de la page. */
export async function expectHeaderState(page: Page, state: string, label: string, timeout = 90_000) {
  const badge = headerState(page);
  await expect(badge).toHaveAttribute("data-state", state, { timeout });
  await expect(badge).toHaveText(label);
}

/**
 * Ouvre depuis la liste des approbations en attente (navigation UI) l'approbation dont le titre
 * est donné. La liste est rechargée tant qu'elle n'apparaît pas (le moteur sonde Hermes toutes les 3 s).
 */
export async function openPendingApproval(page: Page, title: string, timeout = 120_000) {
  await page.getByRole("navigation", { name: "Navigation principale" }).getByRole("link", { name: /^Approbations/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Approbations" })).toBeVisible();
  const link = page.getByRole("link", { name: new RegExp(escapeRe(title)) }).first();
  await expect(async () => {
    if (!(await link.isVisible())) {
      await page.reload();
      await expect(link).toBeVisible({ timeout: 3_000 });
    }
  }).toPass({ timeout, intervals: [1_000, 2_000, 3_000] });
  await link.click();
  await expect(page.getByRole("heading", { level: 1 })).toContainText(title);
}

/** Approuve l'approbation affichée et attend son exécution par le moteur. */
export async function approveCurrent(page: Page, comment = "") {
  if (comment) await page.getByLabel("Commentaire").fill(comment);
  await page.getByRole("button", { name: "Approuver" }).click();
  const outcome = page.getByTestId("approval-outcome");
  await expect(outcome).toHaveAttribute("data-status", "executed", { timeout: 60_000 });
  await expect(outcome).toContainText("Exécutée");
}

export function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
