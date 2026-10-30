import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { setCsrfToken } from "@/api/client";
import type { FeatureDetail, Project, Ticket } from "@/api/types";
import FeatureDetailPage from "@/pages/FeatureDetail";
import FeaturesPage from "@/pages/Features";
import { mockFetch, renderWithProviders } from "./utils";

const project = (id: number, slug: string, integration_mode: Project["integration_mode"]): Project => ({
  id,
  slug,
  name: slug.toUpperCase(),
  repository: `o/${slug}`,
  base_branch: "develop",
  required_checks: [],
  mode: "product",
  enabled: true,
  test_command: "",
  integration_mode,
  created_at: "2026-10-21T10:00:00Z",
});
const projects = { items: [project(1, "fraiszen", "feature_branch"), project(2, "sandbox", "direct")], next_cursor: null };

const ticket = (over: Partial<Ticket>): Ticket => ({
  id: 66,
  feature_id: 6,
  project_id: 1,
  key: "fraiszen-F6-T1",
  title: "Moteur",
  body: "",
  acceptance: [],
  state: "HUMAN_MERGE",
  state_reason: null,
  issue_number: 66,
  issue_url: null,
  branch: "aos/fraiszen-F6-T1",
  head_sha: "1b7facfeb2eee3d7681013a68c8c2be21e30c2f3",
  attempt: 1,
  pr_number: 69,
  pr_url: "https://github.com/o/fraiszen/pull/69",
  ci_state: "success",
  version: 3,
  created_at: "2026-10-21T10:00:00Z",
  ...over,
});

const feature = (over: Partial<FeatureDetail> = {}): FeatureDetail => ({
  id: 6,
  project_id: 1,
  project: "fraiszen",
  title: "Alerte frais réels",
  description: "d",
  state: "IN_DEV",
  state_reason: null,
  spec: null,
  design: null,
  version: 9,
  integration_mode: "direct",
  feature_branch: null,
  feature_pr_number: null,
  feature_pr_url: null,
  tickets: [ticket({}), ticket({ id: 67, key: "fraiszen-F6-T2", state: "READY_FOR_DEV", pr_number: null, pr_url: null })],
  approvals: [],
  runs: [],
  timeline: [],
  created_at: "2026-10-21T10:00:00Z",
  ...over,
});

describe("Mode d'intégration", () => {
  beforeEach(() => setCsrfToken("csrf"));

  it("pré-remplit la création de feature avec le défaut du projet et envoie le choix", async () => {
    const { calls } = mockFetch({
      "GET /api/projects": { body: projects },
      "GET /api/features": { body: { items: [], next_cursor: null } },
      "POST /api/features": { status: 201, body: { id: 7 } },
    });
    const user = userEvent.setup();
    renderWithProviders(<FeaturesPage />, { route: "/features", path: "/features" });
    await user.click(await screen.findByRole("button", { name: /Nouvelle feature/ }));
    const dialog = within(await screen.findByRole("dialog"));
    const integration = dialog.getByRole("combobox", { name: "Intégration" });
    expect(integration).toHaveValue("feature_branch");
    await user.selectOptions(dialog.getByRole("combobox", { name: /^Projet/ }), "2");
    expect(integration).toHaveValue("direct");
    await user.selectOptions(integration, "feature_branch");
    await user.type(dialog.getByRole("textbox", { name: /Titre/ }), "Export CSV");
    await user.type(dialog.getByRole("textbox", { name: /Description/ }), "Exporter les frais");
    await user.click(screen.getByRole("button", { name: "Créer" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({
      project_id: 2,
      title: "Export CSV",
      description: "Exporter les frais",
      integration_mode: "feature_branch",
    });
  });

  it("bascule une feature en cours en branche de feature, avec la PR ouverte redirigée", async () => {
    const { calls } = mockFetch({
      "GET /api/projects": { body: projects },
      "GET /api/features/6": { body: feature() },
      "POST /api/features/6/integration": { body: feature({ integration_mode: "feature_branch", feature_branch: "feature/F6-alerte-frais-reels" }) },
    });
    const user = userEvent.setup();
    renderWithProviders(<FeatureDetailPage />, { route: "/features/6", path: "/features/:id" });
    expect(await screen.findByTestId("integration-mode")).toHaveTextContent("Directe");
    await user.click(screen.getByRole("button", { name: "Passer en branche de feature" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("crée la branche de feature depuis develop et redirige 1 PR déjà ouverte(s) vers elle");
    await user.click(screen.getByRole("button", { name: "Changer" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = calls.find((c) => c.method === "POST")!;
    expect(post.url).toBe("/api/features/6/integration");
    expect(post.body).toEqual({ mode: "feature_branch" });
  });

  it("verrouille le mode dès qu'un ticket est mergé et affiche branche et PR finale", async () => {
    mockFetch({
      "GET /api/projects": { body: projects },
      "GET /api/features/6": {
        body: feature({
          state: "ACCEPTANCE",
          integration_mode: "feature_branch",
          feature_branch: "feature/F6-alerte-frais-reels",
          feature_pr_number: 80,
          feature_pr_url: "https://github.com/o/fraiszen/pull/80",
          tickets: [ticket({ state: "DONE" })],
        }),
      },
    });
    renderWithProviders(<FeatureDetailPage />, { route: "/features/6", path: "/features/:id" });
    expect(await screen.findByTestId("integration-mode")).toHaveTextContent("Branche de feature");
    expect(screen.getByTestId("integration-branch")).toHaveTextContent("feature/F6-alerte-frais-reels");
    expect(screen.getByRole("link", { name: /PR finale #80/ })).toHaveAttribute("href", "https://github.com/o/fraiszen/pull/80");
    expect(screen.getByText(/Mode verrouillé : Feature en recette/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Passer en/ })).not.toBeInTheDocument();
    expect(screen.getAllByText("Recette humaine").length).toBeGreaterThan(0);
  });
});

describe("Recette : demander une correction", () => {
  beforeEach(() => setCsrfToken("csrf"));
  const inAcceptance = (over: Partial<FeatureDetail> = {}) =>
    feature({
      state: "ACCEPTANCE",
      integration_mode: "feature_branch",
      feature_branch: "feature/F6-alerte-frais-reels",
      feature_pr_number: 72,
      feature_pr_url: "https://github.com/o/fraiszen/pull/72",
      tickets: [ticket({ state: "DONE" })],
      ...over,
    });

  it("crée un ticket de correction avec description et critères, la PR finale restant ouverte", async () => {
    const { calls } = mockFetch({
      "GET /api/projects": { body: projects },
      "GET /api/features/6": { body: inAcceptance() },
      "POST /api/features/6/corrections": { status: 201, body: ticket({ id: 70, key: "fraiszen-F6-T4", state: "PLANNED", origin: "correction" }) },
    });
    const user = userEvent.setup();
    renderWithProviders(<FeatureDetailPage />, { route: "/features/6", path: "/features/:id" });
    expect(await screen.findByTestId("acceptance-hint")).toHaveTextContent("Demander une correction");
    await user.click(screen.getByRole("button", { name: "Demander une correction" }));
    const dialog = within(await screen.findByRole("dialog"));
    expect(dialog.getByText(/La PR finale #72 reste ouverte et suivra la branche/)).toBeInTheDocument();
    // Validation : titre et description requis.
    await user.click(dialog.getByRole("button", { name: "Créer le ticket de correction" }));
    expect(dialog.getByText("Le titre est requis.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await user.type(dialog.getByRole("textbox", { name: /Titre/ }), "Récapitulatif : forfait absent");
    await user.type(dialog.getByRole("textbox", { name: /Problème constaté/ }), "Sur /summary le bloc forfait 10 % ne s'affiche pas.");
    await user.type(dialog.getByRole("textbox", { name: "Critère 1" }), "Le bloc forfait 10 % s'affiche");
    await user.click(dialog.getByRole("button", { name: "Ajouter un critère" }));
    await user.type(dialog.getByRole("textbox", { name: "Critère 2" }), "   ");
    await user.click(dialog.getByRole("button", { name: "Créer le ticket de correction" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = calls.find((c) => c.method === "POST")!;
    expect(post.url).toBe("/api/features/6/corrections");
    expect(post.body).toEqual({
      title: "Récapitulatif : forfait absent",
      description: "Sur /summary le bloc forfait 10 % ne s'affiche pas.",
      acceptance_criteria: ["Le bloc forfait 10 % s'affiche"],
    });
  });

  it("propose la correction depuis un blocage en recette, jamais hors recette ni en mode direct ; marque les tickets de correction", async () => {
    mockFetch({
      "GET /api/projects": { body: projects },
      "GET /api/features/6": {
        body: inAcceptance({
          state: "BLOCKED",
          previous_state: "ACCEPTANCE",
          feature_pr_number: null,
          feature_pr_url: null,
          tickets: [ticket({ state: "DONE" }), ticket({ id: 70, key: "fraiszen-F6-T4", state: "DONE", origin: "correction" })],
        }),
      },
    });
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(<FeatureDetailPage />, { route: "/features/6", path: "/features/:id" });
    await user.click(await screen.findByRole("button", { name: "Demander une correction" }));
    expect(await screen.findByText(/La PR finale sera proposée au retour en recette/)).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("tab", { name: /Tickets/ }));
    expect(screen.getAllByTestId("ticket-correction")).toHaveLength(1);
    unmount();

    for (const f of [feature({ state: "IN_DEV", integration_mode: "feature_branch", feature_branch: "feature/F6-x" }), feature({ state: "ACCEPTANCE" })]) {
      mockFetch({ "GET /api/projects": { body: projects }, "GET /api/features/6": { body: f } });
      const r = renderWithProviders(<FeatureDetailPage />, { route: "/features/6", path: "/features/:id" });
      expect(await screen.findByTestId("integration-mode")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Demander une correction" })).not.toBeInTheDocument();
      r.unmount();
    }
  });
});
