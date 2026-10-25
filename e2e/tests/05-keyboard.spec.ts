import { expect, expectVisibleFocus, tabTo, test, workerApi } from "../support/env";

// Navigateur sans session : la connexion elle-même se fait au clavier.
test.use({ storageState: { cookies: [], origins: [] } });

test("clavier seul : connexion puis décision d'approbation, focus visible", async ({ page, playwright, e2e }) => {
  // Approbation en attente préparée par l'API (session administrateur partagée).
  const { api, dispose } = await workerApi(playwright);
  const projectId = await api.ensureProject("divers", "o/divers");
  const featureId = await api.createFeature(projectId, "Décision au clavier");
  await api.featureAction(featureId, "start_spec");
  const approval = await api.pendingApproval(featureId, "spec_review");
  await dispose();

  // --- Connexion : champ identifiant focalisé d'office, Tab, Entrée.
  await page.goto(`/approvals/${approval.id}`);
  await expect(page).toHaveURL(/\/login$/);
  const loginField = page.getByLabel("Identifiant");
  await expectVisibleFocus(loginField);
  await page.keyboard.type(e2e.login);
  await page.keyboard.press("Tab");
  await expectVisibleFocus(page.getByLabel("Mot de passe"));
  await page.keyboard.type(e2e.password);
  await page.keyboard.press("Tab");
  await expectVisibleFocus(page.getByRole("button", { name: "Se connecter" }));
  await page.keyboard.press("Enter");
  // Retour à la page demandée avant la connexion.
  await expect(page).toHaveURL(new RegExp(`/approvals/${approval.id}$`));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Spécification : Décision au clavier");

  // --- Lien d'évitement puis décision au clavier.
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: "Aller au contenu" });
  await expectVisibleFocus(skip);
  await expect(skip).toBeInViewport();
  await page.keyboard.press("Enter");
  const comment = page.getByLabel("Commentaire");
  await tabTo(page, comment);
  await expectVisibleFocus(comment);
  await page.keyboard.type("Validé au clavier.");
  const approve = page.getByRole("button", { name: "Approuver" });
  await tabTo(page, approve);
  await expectVisibleFocus(approve);
  await page.keyboard.press("Enter");
  const outcome = page.getByTestId("approval-outcome");
  await expect(outcome).toHaveAttribute("data-status", /^(approved|executed)$/);
  await expect(outcome).toContainText("Validé au clavier.");
  await expect(outcome).toHaveAttribute("data-status", "executed", { timeout: 30_000 });
});
