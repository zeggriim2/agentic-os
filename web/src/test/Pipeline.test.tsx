import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import type { Pipeline } from "@/api/types";
import PipelinePage from "@/pages/Pipeline";
import { mockFetch, renderWithProviders } from "./utils";

const pipeline: Pipeline = {
  projects: [
    { id: 1, slug: "shop" },
    { id: 2, slug: "fraiszen" },
  ],
  features: {
    columns: [
      {
        state: "SPEC_REVIEW",
        label: "Revue de spec",
        items: [
          {
            type: "feature",
            id: 3,
            title: "Export CSV",
            project: "shop",
            state: "SPEC_REVIEW",
            updated_at: "2026-10-21T10:00:00Z",
            attention: true,
          },
        ],
      },
      {
        state: "IN_DEV",
        label: "Tickets en cours",
        items: [
          {
            type: "feature",
            id: 6,
            title: "Alerte frais réels",
            project: "fraiszen",
            state: "IN_DEV",
            updated_at: "2026-10-21T11:00:00Z",
            attention: false,
            progress: {
              total: 3,
              done: 1,
              current: { key: "fraiszen-F6-T2", state: "REVIEW" },
            },
          },
        ],
      },
      { state: "DONE", label: "Terminée", items: [] },
    ],
  },
  tickets: {
    columns: [
      {
        state: "REVIEW",
        label: "Revue de code",
        items: [
          {
            type: "ticket",
            id: 11,
            title: "shop-F3-T1 · Endpoint export",
            project: "shop",
            feature_id: 3,
            feature_title: "Export CSV",
            state: "REVIEW",
            updated_at: "2026-10-21T09:00:00Z",
            attention: false,
          },
          {
            type: "ticket",
            id: 21,
            title: "fraiszen-F6-T2 · Alerte",
            project: "fraiszen",
            feature_id: 6,
            feature_title: "Alerte frais réels",
            state: "REVIEW",
            updated_at: "2026-10-21T09:30:00Z",
            attention: false,
          },
        ],
      },
      {
        state: "BLOCKED",
        label: "Bloqué",
        items: [
          {
            type: "ticket",
            id: 22,
            title: "fraiszen-F6-T3 · Persistance",
            project: "fraiszen",
            feature_id: 6,
            feature_title: "Alerte frais réels",
            state: "BLOCKED",
            updated_at: "2026-10-21T09:40:00Z",
            attention: true,
          },
        ],
      },
      { state: "DONE", label: "Terminé", items: [] },
    ],
  },
};

const render = (route = "/pipeline") => renderWithProviders(<PipelinePage />, { route, path: "/pipeline" });

describe("Pipeline", () => {
  beforeEach(() => localStorage.clear());

  it("ouvre le tableau des features, sans colonnes de ticket ni étapes vides, avec l'avancement", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    render();
    const cols = await screen.findAllByTestId("pipeline-column");
    expect(cols).toHaveLength(2);
    expect(within(cols[0]!).getByRole("heading", { name: "Revue de spec" })).toBeInTheDocument();
    expect(within(cols[1]!).getByRole("heading", { name: "Tickets en cours" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Revue de code" })).not.toBeInTheDocument();
    expect(screen.getByTestId("feature-progress")).toHaveTextContent("1/3 mergé · T2 : revue de code");
    const feature = screen.getByText("Export CSV").closest("a")!;
    expect(feature).toHaveAttribute("href", "/features/3");
    expect(within(feature).getByText("Attention")).toBeInTheDocument();
    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(tabs[0]).toHaveTextContent("Features1 !2");
    expect(tabs[1]).toHaveTextContent("Tickets1 !3");
  });

  it("bascule sur les tickets, qui rappellent leur feature", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    const user = userEvent.setup();
    render();
    await user.click(await screen.findByRole("tab", { name: /Tickets/ }));
    const cols = screen.getAllByTestId("pipeline-column");
    expect(cols.map((c) => within(c).getByRole("heading").textContent)).toEqual(["Revue de code", "Bloqué"]);
    const ticket = screen.getByText("shop-F3-T1 · Endpoint export").closest("a")!;
    expect(ticket).toHaveAttribute("href", "/tickets/11");
    expect(within(ticket).getByText("Feature : Export CSV")).toBeInTheDocument();
  });

  it("« Voir les tickets » filtre le tableau des tickets sur la feature, filtre retirable", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    const user = userEvent.setup();
    render();
    await user.click(await screen.findByRole("button", { name: "Voir les tickets →" }));
    expect(screen.getByRole("tab", { name: /Tickets/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("feature-filter")).toHaveTextContent("Feature : Alerte frais réels");
    expect(screen.getAllByTestId("pipeline-card")).toHaveLength(2);
    expect(screen.queryByText("shop-F3-T1 · Endpoint export")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retirer le filtre de feature" }));
    expect(screen.getAllByTestId("pipeline-card")).toHaveLength(3);
  });

  it("ouvre directement le filtre de feature depuis l'URL", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    render("/pipeline?tab=tickets&feature=3");
    expect(await screen.findByTestId("feature-filter")).toHaveTextContent("Export CSV");
    expect(screen.getAllByTestId("pipeline-card")).toHaveLength(1);
  });

  it("filtre par projet dans les deux tableaux et mémorise le choix", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    const user = userEvent.setup();
    const { unmount } = render();
    await user.selectOptions(await screen.findByRole("combobox", { name: "Projet" }), "fraiszen");
    expect(screen.getAllByTestId("pipeline-card")).toHaveLength(1);
    expect(screen.getAllByRole("tab")[1]).toHaveTextContent("Tickets1 !2");
    unmount();
    render();
    expect(await screen.findByRole("combobox", { name: "Projet" })).toHaveValue("fraiszen");
    expect(screen.queryByText("Export CSV")).not.toBeInTheDocument();
  });

  it("ignore un projet mémorisé qui n'existe plus", async () => {
    localStorage.setItem("aos.pipeline.project", "disparu");
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    render();
    expect(await screen.findByRole("combobox", { name: "Projet" })).toHaveValue("");
    expect(screen.getAllByTestId("pipeline-card")).toHaveLength(2);
  });

  it("affiche les étapes vides à la demande et mémorise le choix", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    const user = userEvent.setup();
    const { unmount } = render();
    await user.click(
      await screen.findByRole("button", {
        name: "Afficher les étapes vides (1)",
      }),
    );
    const cols = screen.getAllByTestId("pipeline-column");
    expect(cols).toHaveLength(3);
    expect(within(cols[2]!).getByText("Vide")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Masquer les étapes vides" })).toHaveAttribute("aria-pressed", "true");
    unmount();
    render();
    expect(await screen.findAllByTestId("pipeline-column")).toHaveLength(3);
  });

  it("n'offre pas de glisser-déposer", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    render();
    const cards = await screen.findAllByTestId("pipeline-card");
    for (const c of cards) expect(c).not.toHaveAttribute("draggable", "true");
  });

  it("affiche un état vide", async () => {
    const empty = { columns: [{ state: "IDEA", label: "Idées", items: [] }] };
    mockFetch({
      "GET /api/pipeline": {
        body: { features: empty, tickets: { columns: [] }, projects: [] },
      },
    });
    render();
    expect(await screen.findByText("Pipeline vide")).toBeInTheDocument();
  });

  it("affiche une erreur avec possibilité de réessayer", async () => {
    mockFetch({
      "GET /api/pipeline": {
        status: 503,
        body: {
          error: {
            code: "db_unavailable",
            message: "Base de données indisponible.",
          },
        },
      },
    });
    render();
    expect(await screen.findByRole("alert")).toHaveTextContent("Base de données indisponible.");
    expect(screen.getByRole("button", { name: "Réessayer" })).toBeInTheDocument();
  });
});
