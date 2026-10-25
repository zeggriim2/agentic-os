import { expect, test } from "../support/env";
import { expectHeaderState } from "../support/ui";

test.describe("rejets d'approbation", () => {
  let projectId: number;

  test.beforeEach(async ({ api }) => {
    projectId = await api.ensureProject("rejet", "o/rejet");
  });

  test("rejet de la spec avec commentaire : retour du feedback au PO puis nouvelle revue", async ({ page, api }) => {
    const title = "Spec à préciser";
    const id = await api.createFeature(projectId, title);
    await api.featureAction(id, "start_spec");
    const first = await api.pendingApproval(id, "spec_review");

    await page.goto(`/approvals/${first.id}`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(`Spécification : ${title}`);
    // Le commentaire est obligatoire pour rejeter (contrôle UI, rien n'est envoyé).
    await page.getByRole("button", { name: "Rejeter" }).click();
    await expect(page.getByText("Un commentaire est obligatoire pour rejeter.")).toBeVisible();
    await expect(page.getByLabel("Commentaire")).toHaveAttribute("aria-invalid", "true");
    expect((await api.get(`/api/approvals/${first.id}`)).status).toBe("pending");

    const comment = "Préciser le format du fichier attendu (Markdown).";
    await page.getByLabel("Commentaire").fill(comment);
    await page.getByRole("button", { name: "Rejeter" }).click();
    // Décision enregistrée (« rejetée ») puis effet appliqué par le moteur (« Rejet exécuté ») :
    // dans les deux cas, l'UI présente un rejet, jamais une simple « Exécutée ».
    const outcome = page.getByTestId("approval-outcome");
    await expect(outcome).toHaveAttribute("data-status", /^(rejected|executed)$/);
    await expect(outcome).toContainText(/Décision enregistrée — rejetée|Rejet exécuté/);
    await expect(outcome).toContainText(comment);
    await expect(outcome).toHaveAttribute("data-status", "executed", { timeout: 30_000 });
    await expect(outcome).toContainText("Rejet exécuté");
    await expect(page.locator("main header [data-state]").first()).toHaveText("Rejet exécuté");

    // La feature repasse en SPEC, le PO reçoit le commentaire et une nouvelle revue est demandée.
    const second = await api.pendingApproval(id, "spec_review");
    expect(second.id).not.toBe(first.id);
    await page.goto(`/features/${id}`);
    await expectHeaderState(page, "SPEC_REVIEW", "Revue spec");
    await expect(page.getByRole("tabpanel")).toContainText("Spécification révisée selon le retour humain");
    const approvals = page.locator(".card").filter({ has: page.getByRole("heading", { name: "Approbations", level: 2 }) });
    await expect(approvals.locator('[data-state="executed"]')).toHaveText("Rejet exécuté");
    await expect(approvals.locator('[data-state="pending"]')).toHaveText("En attente");
    // Le rejet est audité (sur la feature) avec son commentaire.
    const audit = await api.get(`/api/audit?subject_type=feature&subject_id=${id}`);
    const rejected = audit.items.find((e: any) => e.action === "approval.rejected");
    expect(rejected?.after).toMatchObject({ approval_id: first.id, comment });
  });

  test("rejet du feu vert développement : feature et tickets annulés, aucune branche ni PR", async ({ page, api, fakes }) => {
    const title = "Feature refusée au feu vert";
    const id = await api.createFeature(projectId, title);
    await api.featureAction(id, "start_spec");
    await api.decide((await api.pendingApproval(id, "spec_review")).id, "approved");
    await api.decide((await api.pendingApproval(id, "design_review")).id, "approved");
    const gate = await api.pendingApproval(id, "dev_gate");

    await page.goto(`/approvals/${gate.id}`);
    await page.getByLabel("Commentaire").fill("Pas maintenant : priorité à un autre sujet.");
    await page.getByRole("button", { name: "Rejeter" }).click();
    await expect(page.getByTestId("approval-outcome")).toHaveAttribute("data-status", /rejected|executed/);
    // Le moteur exécute le rejet (job execute_approval durable).
    await expect.poll(async () => (await api.get(`/api/approvals/${gate.id}`)).status, { timeout: 60_000 }).toBe("executed");
    await api.waitFeatureState(id, "CANCELLED", 30_000);

    await page.goto(`/features/${id}`);
    await expectHeaderState(page, "CANCELLED", "Annulé", 60_000);
    await page.getByRole("tab", { name: /Tickets/ }).click();
    const states = page.getByRole("tabpanel").locator("tbody [data-state]");
    await expect(states.first()).toHaveAttribute("data-state", "CANCELLED");
    await expect(states.first()).toHaveText("Annulé");
    // Aucune action « Annuler » ne reste proposée sur une feature terminale.
    await expect(page.getByRole("button", { name: "Annuler" })).toHaveCount(0);
    expect((await fakes.state()).pulls["o/rejet"] ?? []).toHaveLength(0);
  });
});
