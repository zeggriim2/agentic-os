import { expect, test } from "../support/env";
import { expectHeaderState } from "../support/ui";

test.describe("rejets d'approbation", () => {
  let projectId: number;

  test.beforeEach(async ({ api }) => {
    projectId = await api.ensureProject("rejet", "o/rejet");
  });

  test("réponses aux questions du PO : un clic + champ libre, retour structuré puis nouvelle revue", async ({ page, api }) => {
    const title = "Spec à préciser";
    const id = await api.createFeature(projectId, title);
    await api.featureAction(id, "start_spec");
    const first = await api.pendingApproval(id, "spec_review");

    await page.goto(`/approvals/${first.id}`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(`Spécification : ${title}`);
    // Rien à envoyer tant qu'aucune réponse n'est donnée (rien n'est envoyé).
    const send = page.getByRole("button", { name: "Envoyer mes réponses au PO" });
    await expect(send).toBeDisabled();
    const card = page.getByTestId("question-card");
    await expect(card).toContainText("Quel format pour le fichier de démonstration ?");
    await expect(card.getByRole("button", { name: /Markdown/ })).toContainText("recommandé");
    await card.getByRole("button", { name: /Texte brut/ }).click();
    await card.getByLabel("Ta réponse à la question 1").fill("avec la date du jour");
    await expect(page.getByTestId("answered-count")).toHaveText("1 / 1 traitée");
    expect((await api.get(`/api/approvals/${first.id}`)).status).toBe("pending");

    // Brouillon conservé après rechargement.
    await page.reload();
    await expect(page.getByTestId("question-card").getByRole("button", { name: /Texte brut/ })).toHaveAttribute("aria-pressed", "true");
    await page.getByTestId("decision-comment").fill("Garder un seul fichier.");
    await send.click();
    // Décision enregistrée puis exécutée : l'UI présente un rejet (renvoi au PO), jamais « Exécutée ».
    const outcome = page.getByTestId("approval-outcome");
    await expect(outcome).toHaveAttribute("data-status", /^(rejected|executed)$/);
    await expect(outcome).toContainText("Texte brut — avec la date du jour");
    await expect(outcome).toHaveAttribute("data-status", "executed", { timeout: 30_000 });
    await expect(outcome).toContainText("Rejet exécuté");

    // La feature repasse en SPEC, le PO reçoit les réponses et une nouvelle revue est demandée.
    const second = await api.pendingApproval(id, "spec_review");
    expect(second.id).not.toBe(first.id);
    await page.goto(`/features/${id}`);
    await expectHeaderState(page, "SPEC_REVIEW", "Revue spec");
    await expect(page.getByRole("tabpanel")).toContainText("Spécification révisée selon le retour humain");
    // Le renvoi est audité avec le retour structuré question → réponse.
    const audit = await api.get(`/api/audit?subject_type=feature&subject_id=${id}`);
    const rejected = audit.items.find((e: any) => e.action === "approval.rejected");
    expect(rejected?.after?.approval_id).toBe(first.id);
    expect(rejected?.after?.comment).toContain("1. Quel format pour le fichier de démonstration ?\n   → Texte brut — avec la date du jour");
    expect(rejected?.after?.comment).toContain("Remarque générale : Garder un seul fichier.");
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
    await page.getByRole("button", { name: "Refuser et annuler les tickets" }).click();
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
