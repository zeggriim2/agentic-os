import { expect, test } from "../support/env";
import { expectHeaderState } from "../support/ui";

// Recette humaine : un défaut constaté sur la branche de feature devient un ticket de correction
// (bouton « Demander une correction »). La feature repart en développement, le ticket est mergé dans
// la branche de feature, puis la feature revient en recette avec la PR finale existante mise à jour.
test("recette : demander une correction, retour en recette, PR finale mise à jour puis mergée", async ({ page, api, fakes }) => {
  test.setTimeout(300_000);
  const repo = "o/corr";
  const projectId = await api.ensureProject("corr", repo);
  await api.send("PATCH", `/api/projects/${projectId}`, { integration_mode: "feature_branch" });
  const id = await api.createFeature(projectId, "Récapitulatif annuel");
  await api.featureAction(id, "start_spec");
  await api.decide((await api.pendingApproval(id, "spec_review")).id, "approved");
  await api.decide((await api.pendingApproval(id, "design_review")).id, "approved");
  await api.decide((await api.pendingApproval(id, "dev_gate")).id, "approved");

  const tickets = async () => (await api.feature(id)).tickets as any[];
  const mergeTicket = async (key: string) => {
    const tk = async () => (await tickets()).find((t) => t.key === key);
    await expect.poll(async () => (await tk()).state, { timeout: 120_000 }).toBe("AWAITING_PR_APPROVAL");
    const t = await tk();
    const publish = (await api.get(`/api/tickets/${t.id}`)).approvals.find((a: any) => a.kind === "publish_pr" && a.status === "pending");
    await api.decide(publish.id, "approved");
    await expect.poll(async () => (await tk()).pr_number, { timeout: 60_000 }).toBeTruthy();
    expect((await fakes.post("/__e2e/merge", { repo, number: (await tk()).pr_number })).status).toBe(200);
    await expect.poll(async () => (await tk()).state, { timeout: 60_000 }).toBe("DONE");
  };
  await mergeTicket((await tickets())[0].key);
  await api.waitFeatureState(id, "ACCEPTANCE", 60_000);
  await api.decide((await api.pendingApproval(id, "publish_feature_pr")).id, "approved");
  const featurePR = async () => (await api.feature(id)).feature.feature_pr_number as number | null;
  await expect.poll(featurePR, { timeout: 60_000 }).not.toBeNull();
  const finalNumber = (await featurePR())!;

  // L'humain teste la branche et constate un défaut : bouton, formulaire, ticket créé.
  await page.goto(`/features/${id}`);
  await expectHeaderState(page, "ACCEPTANCE", "Recette humaine", 30_000);
  await expect(page.getByTestId("acceptance-hint")).toBeVisible();
  await page.getByRole("button", { name: "Demander une correction" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText(`La PR finale #${finalNumber} reste ouverte`);
  await dialog.getByRole("textbox", { name: /Titre/ }).fill("Récapitulatif : forfait 10 % absent");
  await dialog.getByRole("textbox", { name: /Problème constaté/ }).fill("Sur /summary, le bloc forfait 10 % ne s'affiche pas.");
  await dialog.getByRole("textbox", { name: "Critère 1" }).fill("Le bloc forfait 10 % s'affiche sur /summary");
  await dialog.getByRole("button", { name: "Créer le ticket de correction" }).click();
  await expect(dialog).toBeHidden();
  await expectHeaderState(page, "IN_DEV", "Tickets en cours", 30_000);
  await expect(page.getByRole("button", { name: "Demander une correction" })).toHaveCount(0);
  await page.getByRole("tab", { name: /Tickets/ }).click();
  await expect(page.getByTestId("ticket-correction")).toHaveCount(1);

  const correction = (await tickets()).find((t) => t.origin === "correction");
  expect(correction.acceptance).toEqual(["Le bloc forfait 10 % s'affiche sur /summary"]);
  await expect.poll(async () => (await tickets()).find((t) => t.id === correction.id).issue_number, { timeout: 60_000 }).toBeTruthy();
  const issue = (await tickets()).find((t) => t.id === correction.id).issue_number;
  await mergeTicket(correction.key);
  const cpr = (await fakes.state()).pulls[repo].find((p: any) => p.head.ref === `aos/${correction.key}`);
  expect(cpr.base.ref).toBe((await api.feature(id)).feature.feature_branch);

  // Retour en recette : même PR finale, corps mis à jour avec la correction, aucune nouvelle approbation.
  await api.waitFeatureState(id, "ACCEPTANCE", 60_000);
  await expect
    .poll(async () => (await fakes.state()).pulls[repo].find((p: any) => p.number === finalNumber).body, { timeout: 60_000 })
    .toContain(`Closes #${issue}`);
  expect(await featurePR()).toBe(finalNumber);
  const pending = (await api.feature(id)).approvals.filter((a: any) => a.status === "pending");
  expect(pending).toHaveLength(0);
  await page.reload();
  await expectHeaderState(page, "ACCEPTANCE", "Recette humaine", 30_000);

  expect((await fakes.post("/__e2e/merge", { repo, number: finalNumber })).status).toBe(200);
  await api.waitFeatureState(id, "DONE", 60_000);
  await expectHeaderState(page, "DONE", "Terminé", 30_000);
});
