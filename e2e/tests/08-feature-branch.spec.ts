import { expect, test } from "../support/env";
import { expectHeaderState } from "../support/ui";

// Mode d'intégration « branche de feature » : la PR du ticket vise la branche de feature, la feature
// passe en recette quand tous les tickets y sont mergés, puis la PR finale vers main la termine.
test("branche de feature : PR du ticket vers la branche, recette, PR finale vers main → DONE", async ({ page, api, fakes }) => {
  test.setTimeout(240_000);
  const repo = "o/integ";
  const projectId = await api.ensureProject("integ", repo);
  const project = await api.send("PATCH", `/api/projects/${projectId}`, { integration_mode: "feature_branch" });
  expect(project.integration_mode).toBe("feature_branch");

  const id = await api.createFeature(projectId, "Intégration groupée");
  expect((await api.feature(id)).feature?.integration_mode ?? (await api.feature(id)).integration_mode).toBe("feature_branch");
  await api.featureAction(id, "start_spec");
  await api.decide((await api.pendingApproval(id, "spec_review")).id, "approved");
  await api.decide((await api.pendingApproval(id, "design_review")).id, "approved");
  const gate = await api.pendingApproval(id, "dev_gate");
  expect((await api.get(`/api/approvals/${gate.id}`)).effect).toContain("Intégration : branche de feature feature/F");
  await api.decide(gate.id, "approved");

  // Le ticket est développé depuis la branche de feature, puis sa PR la vise.
  const branchOf = async () => {
    const f = await api.feature(id);
    return (f.feature ?? f).feature_branch as string | null;
  };
  await expect.poll(branchOf, { timeout: 60_000 }).toMatch(/^feature\/F\d+-integration-groupee$/);
  const branch = (await branchOf())!;
  const ticketOf = async () => (await api.feature(id)).tickets[0];
  await expect.poll(async () => (await ticketOf()).state, { timeout: 120_000 }).toBe("AWAITING_PR_APPROVAL");
  const ticket = await ticketOf();
  const publish = (await api.get(`/api/tickets/${ticket.id}`)).approvals.find((a: any) => a.kind === "publish_pr" && a.status === "pending");
  expect(publish.effect).toContain(`vers ${branch}`);
  await api.decide(publish.id, "approved");
  await expect.poll(async () => (await ticketOf()).state, { timeout: 60_000 }).toBe("PR_OPENED");
  const pulls = (await fakes.state()).pulls[repo];
  const pr = pulls.find((p: any) => p.head.ref === `aos/${ticket.key}`);
  expect(pr).toMatchObject({ draft: true, base: { ref: branch } });
  expect(pr.body).toContain("Refs #");
  expect(pr.body).not.toContain("Closes #");

  // Merge du ticket dans la branche de feature : la feature passe en recette.
  expect((await fakes.post("/__e2e/merge", { repo, number: pr.number })).status).toBe(200);
  await api.waitFeatureState(id, "ACCEPTANCE", 60_000);
  await page.goto(`/features/${id}`);
  await expectHeaderState(page, "ACCEPTANCE", "Recette humaine", 30_000);
  await expect(page.getByTestId("integration-mode")).toHaveText("Branche de feature");
  await expect(page.getByTestId("integration-branch")).toHaveText(branch);
  await expect(page.getByText(/Mode verrouillé/)).toBeVisible();

  // PR finale (approbation) puis merge humain : feature terminée.
  const final = await api.pendingApproval(id, "publish_feature_pr");
  expect(final.title).toBe("Publier la PR finale : Intégration groupée");
  await api.decide(final.id, "approved");
  const prNumber = async () => {
    const f = await api.feature(id);
    return (f.feature ?? f).feature_pr_number as number | null;
  };
  await expect.poll(prNumber, { timeout: 60_000 }).not.toBeNull();
  const finalNumber = await prNumber();
  const fpr = (await fakes.state()).pulls[repo].find((p: any) => p.number === finalNumber);
  expect(fpr).toMatchObject({ draft: true, head: { ref: branch }, base: { ref: "main" } });
  expect(fpr.body).toContain("Closes #");
  await page.reload();
  await expect(page.getByRole("link", { name: new RegExp(`PR finale #${fpr.number}`) })).toBeVisible();
  const merged = await fakes.post("/__e2e/merge", { repo, number: fpr.number });
  expect(merged).toMatchObject({ event: "pull_request", status: 200 });
  await api.waitFeatureState(id, "DONE", 60_000);
  await expectHeaderState(page, "DONE", "Terminé", 30_000);
});
