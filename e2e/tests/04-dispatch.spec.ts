import { expect, test } from "../support/env";

test("dispatch : pause depuis la vue d'ensemble, alerte visible, reprise depuis l'alerte", async ({ page, api }) => {
  const projectId = await api.ensureProject("divers", "o/divers");
  await page.goto("/");
  const card = page.locator(".card").filter({ has: page.getByRole("heading", { name: "Dispatch" }) });
  const alert = page.getByRole("region", { name: "Alertes" }).getByRole("link", { name: /Le dispatch est en pause/ });
  await expect(card).toContainText("Actif");
  await expect(alert).toHaveCount(0);

  try {
    await card.getByRole("button", { name: "Mettre en pause" }).click();
    await page.getByRole("dialog", { name: "Mettre le dispatch en pause ?" }).getByRole("button", { name: "Mettre en pause" }).click();
    await expect(card).toContainText("En pause");
    await expect(alert).toBeVisible();
    expect((await api.get("/api/overview")).dispatch_paused).toBe(true);

    // Aucun nouveau job n'est pris pendant la pause.
    const id = await api.createFeature(projectId, "Feature pendant la pause");
    await api.featureAction(id, "start_spec");
    await page.waitForTimeout(5_000);
    const f = await api.feature(id);
    expect(f.feature.state).toBe("SPEC");
    expect(f.runs).toHaveLength(0);

    // L'alerte mène à la carte Dispatch, d'où l'on reprend.
    await alert.click();
    await expect(page).toHaveURL(/\/$/);
    await card.getByRole("button", { name: "Reprendre" }).click();
    await page.getByRole("dialog", { name: "Reprendre le dispatch ?" }).getByRole("button", { name: "Reprendre" }).click();
    await expect(card).toContainText("Actif");
    await expect(alert).toHaveCount(0);
    await api.waitFeatureState(id, "SPEC_REVIEW");
  } finally {
    // Ne jamais laisser le serveur partagé en pause.
    await api.send("POST", "/api/system/dispatch", { paused: false });
  }
});
