import { expect, test } from "../support/env";

// Ces tests partent d'un navigateur sans session.
test.use({ storageState: { cookies: [], origins: [] } });

test("connexion : mauvais mot de passe refusé, bon mot de passe accepté, puis déconnexion", async ({ page, e2e }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);

  await page.getByLabel("Identifiant").fill(e2e.login);
  await page.getByLabel("Mot de passe").fill("mauvais-mot-de-passe");
  await page.getByRole("button", { name: "Se connecter" }).click();
  await expect(page.getByRole("alert")).toContainText("identifiant ou mot de passe incorrect");
  await expect(page).toHaveURL(/\/login$/);

  await page.getByLabel("Mot de passe").fill(e2e.password);
  await page.getByRole("button", { name: "Se connecter" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1, name: "Vue d'ensemble" })).toBeVisible();
  const me = await page.request.get("/api/auth/me");
  expect(me.status()).toBe(200);

  await page.getByRole("button", { name: "Se déconnecter" }).click();
  await expect(page).toHaveURL(/\/login$/);
  // La session est bien détruite côté serveur.
  expect((await page.request.get("/api/auth/me")).status()).toBe(401);
  await page.goto("/features");
  await expect(page).toHaveURL(/\/login$/);
});
