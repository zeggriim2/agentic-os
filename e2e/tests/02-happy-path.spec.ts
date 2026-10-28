import { expect, test } from "../support/env";
import { approveCurrent, expectHeaderState, openPendingApproval } from "../support/ui";

const FEATURE = "Fichier de démonstration";
const TICKET = "Ajouter le fichier de démonstration";

test("parcours complet par l'UI : projet → feature → spec → design → dev/review/QA → PR draft → merge → DONE", async ({ page, fakes }) => {
  test.setTimeout(300_000);
  const nav = page.getByRole("navigation", { name: "Navigation principale" });

  // --- Projet (créé désactivé puis activé avec confirmation)
  await page.goto("/");
  await nav.getByRole("link", { name: "Projets" }).click();
  await page.getByRole("button", { name: "Nouveau projet" }).click();
  const dialog = page.getByRole("dialog", { name: "Nouveau projet" });
  await dialog.getByLabel("Slug").fill("demo");
  await dialog.getByRole("textbox", { name: "Nom", exact: true }).fill("Démo E2E");
  await dialog.getByLabel("Dépôt GitHub").fill("o/demo");
  await expect(dialog.getByLabel("Branche de base")).toHaveValue("main");
  await dialog.getByLabel("Commande de test").fill("test -f README.md");
  await dialog.getByLabel("Checks CI requis").fill("tests");
  await dialog.getByRole("button", { name: "Créer" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Projet créé" })).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: "o/demo" });
  await expect(row).toContainText("tests");
  await row.getByRole("switch", { name: "Activer demo" }).click();
  await page.getByRole("dialog", { name: "Activer demo ?" }).getByRole("button", { name: "Activer" }).click();
  await expect(row.getByRole("switch", { name: "Désactiver demo" })).toHaveAttribute("aria-checked", "true");

  // --- Feature
  await fakes.hold("po"); // le run PO attendra : on observe le flux en direct
  await nav.getByRole("link", { name: "Features" }).click();
  await page.getByRole("button", { name: "Nouvelle feature" }).click();
  const fd = page.getByRole("dialog", { name: "Nouvelle feature" });
  await fd.getByLabel("Projet").selectOption({ label: "Démo E2E (demo) — sandbox" });
  await fd.getByLabel("Titre").fill(FEATURE);
  await fd.getByLabel("Description").fill("Ajouter un fichier de démonstration au dépôt pour valider la chaîne complète.");
  await fd.getByRole("button", { name: "Créer" }).click();
  await expect(page).toHaveURL(/\/features\/\d+$/);
  const featureURL = page.url();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(FEATURE);
  await expectHeaderState(page, "IDEA", "Idée");

  // --- Démarrer la spec : run PO visible en direct
  await page.getByRole("button", { name: "Démarrer la spec" }).click();
  await page.getByRole("dialog", { name: "Démarrer la spécification ?" }).getByRole("button", { name: "Démarrer" }).click();
  await expectHeaderState(page, "SPEC", "Spécification");
  await page.getByRole("tab", { name: /Runs/ }).click();
  const runLink = page.getByRole("tabpanel").getByRole("link", { name: /^#\d+$/ }).first();
  await expect(async () => {
    if (!(await runLink.isVisible())) {
      await page.reload();
      await page.getByRole("tab", { name: /Runs/ }).click();
      await expect(runLink).toBeVisible({ timeout: 2_000 });
    }
  }).toPass({ timeout: 30_000 });
  await runLink.click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(/Run #\d+ — Product Owner/);
  await expectHeaderState(page, "running", "En cours", 30_000);
  await expect(page.getByText("flux en direct")).toBeVisible();
  const events = page.getByRole("tabpanel").locator("ol li");
  await expect(events.filter({ hasText: "reasoning.available" })).toBeVisible();
  await expect(events.filter({ hasText: "tool.started" }).first()).toBeVisible();
  const before = await events.count();
  // Libération : nouveaux événements reçus sans recharger la page.
  await fakes.release("po");
  await expect(events.filter({ hasText: "run.completed" })).toBeVisible({ timeout: 30_000 });
  await expect(events.filter({ hasText: "tool.completed" })).toBeVisible();
  expect(await events.count()).toBeGreaterThan(before);
  expect(await events.filter({ hasText: "message.delta" }).count()).toBe(0); // deltas non persistés
  await expectHeaderState(page, "completed", "Terminé", 30_000);
  await page.getByRole("tab", { name: "Livrable" }).click();
  await expect(page.getByRole("tabpanel")).toContainText("acceptance_criteria");

  // --- Revue de spec
  await openPendingApproval(page, `Spécification : ${FEATURE}`);
  await expect(page.getByTestId("approval-effect")).toContainText("Valider la spécification");
  await approveCurrent(page);

  // --- Revue de design (effet : création d'issues)
  await openPendingApproval(page, `Conception : ${FEATURE}`);
  await expect(page.getByTestId("approval-effect")).toContainText("Créer 1 issues sur o/demo");
  await approveCurrent(page);

  // --- Feu vert développement (après création réelle des issues dans le faux GitHub)
  await openPendingApproval(page, `Feu vert développement : ${FEATURE}`);
  await expect(page.getByTestId("approval-effect")).toContainText("sans PR ni merge");
  const issues = (await fakes.state()).issues["o/demo"];
  expect(issues).toHaveLength(1);
  expect(issues[0].body).toMatch(/<!-- agentic-os:ticket=demo-F\d+-T1 -->/);
  await approveCurrent(page);

  // --- Développeur → Reviewer → QA, puis approbation de publication
  await openPendingApproval(page, `Publier la PR : ${TICKET}`, 180_000);
  await expect(page.getByTestId("approval-effect")).toContainText(/Créer une PR draft sur o\/demo de aos\/demo-F\d+-T1 vers main/);
  expect((await fakes.state()).pulls["o/demo"] ?? []).toHaveLength(0); // rien n'est publié avant l'approbation
  await approveCurrent(page);

  // --- Ticket : PR draft ouverte, vérifiée dans le faux GitHub
  await page.goto(featureURL);
  await page.getByRole("tab", { name: /Tickets/ }).click();
  const ticketRow = page.getByRole("row").filter({ hasText: TICKET });
  await expect(ticketRow.locator("[data-state]").first()).toHaveAttribute("data-state", "PR_OPENED", { timeout: 60_000 });
  await expect(ticketRow.locator("[data-state]").first()).toHaveText("PR ouverte");
  await ticketRow.getByRole("link", { name: new RegExp(TICKET) }).click();
  await expectHeaderState(page, "PR_OPENED", "PR ouverte");
  const headSha = (await page.locator("dd code").filter({ hasText: /^[0-9a-f]{40}$/ }).first().textContent())!.trim();
  const pulls = (await fakes.state()).pulls["o/demo"];
  expect(pulls).toHaveLength(1);
  const pr = pulls[0];
  expect(pr).toMatchObject({ draft: true, state: "open", merged: false, base: { ref: "main" } });
  expect(pr.head.ref).toMatch(/^aos\/demo-F\d+-T1$/);
  expect(pr.head.sha).toBe(headSha);
  expect(pr.body).toContain(`Closes #${issues[0].number}`);
  await expect(page.getByRole("link", { name: `#${pr.number}` })).toHaveAttribute("href", pr.html_url);
  // Verdicts Reviewer et QA affichés sur le ticket (libellés de web/src/lib/states.ts).
  await expect(page.locator('main [data-state="approved"]').first()).toHaveText("Approuvée");
  await expect(page.locator('main [data-state="passed"]').first()).toHaveText("Validé");

  // --- Pipeline : la feature (Tickets en cours, avancement) et le ticket (PR ouverte) sont dans deux onglets
  const ticketURL = page.url();
  await page.goto("/pipeline");
  await expect(page.getByRole("tab", { name: /Features/ })).toHaveAttribute("aria-selected", "true");
  const fCol = page.getByRole("region", { name: /^Tickets en cours/ });
  await expect(fCol.getByTestId("feature-progress")).toContainText(/0\/1 mergé · T1 : pr ouverte/);
  await expect(page.getByRole("region", { name: /^PR ouverte/ })).toHaveCount(0);
  await fCol.getByRole("button", { name: "Voir les tickets →" }).click();
  await expect(page.getByRole("tab", { name: /Tickets/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("feature-filter")).toBeVisible();
  await expect(page.getByRole("region", { name: /^PR ouverte/ }).getByRole("link", { name: new RegExp(TICKET) })).toBeVisible();
  await page.goto(ticketURL);

  // --- CI verte (webhook check_run signé) → merge humain attendu
  const ci = await fakes.post("/__e2e/webhook/check-run", { repo: "o/demo", number: pr.number });
  expect(ci.status).toBe(200);
  await expectHeaderState(page, "HUMAN_MERGE", "Merge humain", 30_000);

  // --- Merge (webhook pull_request closed+merged signé) → ticket puis feature DONE
  const merged = await fakes.post("/__e2e/merge", { repo: "o/demo", number: pr.number });
  expect(merged).toMatchObject({ event: "pull_request", status: 200, response: { status: "processed", tickets: 1 } });
  await expectHeaderState(page, "DONE", "Terminé", 30_000);
  await page.goto(featureURL);
  await expectHeaderState(page, "DONE", "Terminé", 30_000);

  // Notifications Telegram envoyées au faux Bot API.
  const msgs: string[] = (await fakes.state()).telegram_messages;
  expect(msgs.some((m) => m.includes("PR draft"))).toBe(true);
  expect(msgs.some((m) => m.includes("mergée"))).toBe(true);
});
