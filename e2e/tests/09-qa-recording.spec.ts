import { expect, test } from "../support/env";

// QA navigateur : le faux QA dépose une vraie vidéo et une vraie trace Playwright dans le dossier
// d'enregistrement ; Agentic OS les copie, les sert sur la page du ticket et ouvre la trace dans son
// visualiseur intégré.
test("QA navigateur : enregistrement visible sur le ticket, trace ouverte dans le visualiseur", async ({ page, api }) => {
  test.setTimeout(180_000);
  const projectId = await api.ensureProject("qarec", "o/qarec");
  expect((await api.send("PATCH", `/api/projects/${projectId}`, { qa_browser: true })).qa_browser).toBe(true);
  const id = await api.createFeature(projectId, "Récapitulatif enregistré");
  await api.featureAction(id, "start_spec");
  await api.decide((await api.pendingApproval(id, "spec_review")).id, "approved");
  await api.decide((await api.pendingApproval(id, "design_review")).id, "approved");
  await api.decide((await api.pendingApproval(id, "dev_gate")).id, "approved");
  const ticketOf = async () => (await api.feature(id)).tickets[0];
  await expect.poll(async () => (await ticketOf()).state, { timeout: 120_000 }).toBe("AWAITING_PR_APPROVAL");
  const ticket = await ticketOf();
  const rec = await api.get(`/api/tickets/${ticket.id}/recordings`);
  expect(rec.items).toHaveLength(1);
  expect(rec.items[0].report.ok).toBe(true);

  await page.goto(`/tickets/${ticket.id}`);
  await page.getByRole("tab", { name: /Enregistrement QA/ }).click();
  const session = page.getByTestId(`recording-${rec.items[0].run_id}`);
  await expect(session.getByText("Scénario réussi")).toBeVisible();
  const video = session.getByLabel("Vidéo de la session QA");
  await expect(video).toHaveAttribute("src", `/api/recordings/${rec.items[0].video.id}/file`);
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 }).toBeGreaterThan(0);
  const img = session.getByRole("img", { name: "Récapitulatif (faux QA navigateur)" });
  await expect(img).toBeVisible();
  await expect.poll(() => img.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0);
  await expect(session.getByText("Critère 1")).toBeVisible();
  await session.screenshot({ path: test.info().outputPath("onglet-enregistrement.png") });

  // Visualiseur de traces servi par Agentic OS : la trace se charge (actions enregistrées listées).
  const [viewer] = await Promise.all([page.context().waitForEvent("page"), session.getByRole("link", { name: /Ouvrir la trace/ }).click()]);
  await viewer.waitForLoadState();
  await expect(viewer.getByText(/Set content|setContent/).first()).toBeVisible({ timeout: 30_000 });
  await expect(viewer.getByText(/Could not load trace|Failed to load/i)).toHaveCount(0);
  await viewer.screenshot({ path: test.info().outputPath("visualiseur-trace.png") });
});
