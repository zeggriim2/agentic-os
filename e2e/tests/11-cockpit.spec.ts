import { expect, test } from "../support/env";

// Cockpit (1.11) : tableau en direct, « À décider » (approbation depuis le tiroir), Ctrl+K, fil d'activité.
test("cockpit : la feature avance sur le tableau, décisions depuis le tiroir, Ctrl+K", async ({ page, api }) => {
  test.setTimeout(240_000);
  const projectId = await api.ensureProject("cockpit", "o/cockpit");
  const id = await api.createFeature(projectId, "Tableau en direct");
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "Tableau" })).toBeVisible();
  const decisions = page.getByTestId("decisions-button");

  // La spec est rédigée par le PO puis attend une décision : la carte arrive dans « À toi ».
  await api.featureAction(id, "start_spec");
  const card = page.getByTestId(`board-card-feature-${id}`);
  await expect(page.getByTestId("col-you").getByTestId(`board-card-feature-${id}`)).toBeVisible({ timeout: 90_000 });
  await expect(card).toContainText("Valider la spec");
  await expect(decisions).toHaveAccessibleName(/À décider\s*[1-9]/);
  // Le fil d'activité montre le travail du PO (outil appelé ou fin du run).
  await expect(page.getByTestId("activity-feed")).toContainText("Product Owner");
  await page.screenshot({ path: test.info().outputPath("tableau.png") });

  // Approbation depuis le tiroir, sans quitter le tableau.
  await decisions.click();
  const drawer = page.getByRole("dialog", { name: "À décider" });
  await page.screenshot({ path: test.info().outputPath("a-decider.png") });
  const item = drawer.locator("li").filter({ hasText: "Tableau en direct" }).first();
  await item.getByRole("button", { name: "Approuver" }).click();
  await expect(item).toHaveCount(0, { timeout: 30_000 });
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  // Le tech lead conçoit, puis la conception attend à son tour une décision.
  await expect(card).toContainText("Valider la conception", { timeout: 90_000 });

  // Ctrl+K : aller à la feature au clavier.
  await page.keyboard.press("Control+k");
  const input = page.getByRole("combobox", { name: /Feature, ticket/ });
  await input.fill(`F${id} tableau`);
  await expect(page.getByRole("option", { selected: true })).toContainText(`F${id} · Tableau en direct`);
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/features/${id}$`));
  await expect(page.getByRole("heading", { level: 1, name: "Tableau en direct" })).toBeVisible();
});
