import { expect, test } from "../support/env";

// Discussion avec les agents (1.12) : consigne transmise à l'agent en cours, question reprenant sa session.
test("fil d'équipe : consigne à l'agent en cours, puis question au PO dans sa session", async ({ page, api, fakes }) => {
  test.setTimeout(240_000);
  const projectId = await api.ensureProject("chat", "o/chat");
  const id = await api.createFeature(projectId, "Semaine type");
  await fakes.hold("po");
  try {
    await api.featureAction(id, "start_spec");
    await page.goto(`/features/${id}`);
    const thread = page.getByTestId("feature-thread");
    await expect(thread).toContainText("Au travail", { timeout: 60_000 });

    // Consigne : le PO travaille, elle lui est transmise sans le redémarrer.
    const composer = page.getByTestId("composer");
    await expect(composer.getByLabel("Destinataire")).toHaveValue("");
    await expect(page.getByTestId("composer-hint")).toContainText("transmise tout de suite");
    await composer.getByLabel("Message", { exact: true }).fill("Garde la spec courte, trois critères au plus.");
    await composer.getByRole("button", { name: "Envoyer" }).click();
    const instruction = thread.locator("[data-testid^='thread-instruction-']").last();
    await expect(instruction).toContainText("transmise à l'agent en cours");
    const st = await fakes.state();
    const po = st.runs.find((r: { role: string; status: string }) => r.role === "po" && r.status === "running");
    expect(po?.steers?.[0]).toContain("Garde la spec courte, trois critères au plus.");
  } finally {
    await fakes.release("po");
  }
  await api.waitFeatureState(id, "SPEC_REVIEW");

  // Question au PO : il répond dans sa session sur la feature ; la feature ne bouge pas.
  const composer = page.getByTestId("composer");
  await composer.getByRole("button", { name: "Question" }).click();
  await composer.getByLabel("Destinataire").selectOption("po");
  await expect(page.getByTestId("composer-hint")).toContainText("dans sa session sur la feature");
  await composer.getByLabel("Message", { exact: true }).fill("Pourquoi cinq jours par semaine ?");
  await composer.getByRole("button", { name: "Envoyer" }).click();
  const answer = page.locator("[data-testid^='thread-answer-']").last();
  await expect(answer).toContainText("dans ma session", { timeout: 60_000 });
  await expect(answer).toContainText("Pourquoi cinq jours par semaine ?");
  await expect(answer).toContainText("tokens");
  await expect(page.locator("[data-testid^='thread-question-']").last()).toContainText("répondue");
  await page.screenshot({ path: test.info().outputPath("fil-equipe.png"), fullPage: true });
  expect((await api.feature(id)).feature.state).toBe("SPEC_REVIEW");
});
