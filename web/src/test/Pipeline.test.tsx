import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import type { Pipeline } from "@/api/types";
import PipelinePage from "@/pages/Pipeline";
import { mockFetch, renderWithProviders } from "./utils";

const pipeline: Pipeline = {
  columns: [
    {
      state: "SPEC_REVIEW",
      label: "Revue spec",
      items: [{ type: "feature", id: 3, title: "Export CSV", project: "shop", state: "SPEC_REVIEW", updated_at: "2026-10-21T10:00:00Z", attention: true }],
    },
    {
      state: "REVIEW",
      label: "Revue de code",
      items: [
        { type: "ticket", id: 11, title: "Endpoint export", project: "shop", state: "REVIEW", updated_at: "2026-10-21T09:00:00Z", attention: false },
        { type: "ticket", id: 12, title: "Bouton export", project: "shop", state: "REVIEW", updated_at: "2026-10-21T09:30:00Z", attention: false },
      ],
    },
    { state: "DONE", label: "Terminé", items: [] },
  ],
};

describe("Pipeline", () => {
  beforeEach(() => localStorage.clear());

  it("rend les colonnes et cartes à partir de la réponse API, sans les étapes vides", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    const cols = await screen.findAllByTestId("pipeline-column");
    expect(cols).toHaveLength(2);
    expect(within(cols[0]!).getByRole("heading", { name: "Revue spec" })).toBeInTheDocument();
    expect(within(cols[1]!).getAllByTestId("pipeline-card")).toHaveLength(2);
    expect(screen.queryByRole("heading", { name: "Terminé" })).not.toBeInTheDocument();

    const feature = screen.getByText("Export CSV").closest("a")!;
    expect(feature).toHaveAttribute("href", "/features/3");
    expect(within(feature).getByText("Attention")).toBeInTheDocument();
    const ticket = screen.getByText("Endpoint export").closest("a")!;
    expect(ticket).toHaveAttribute("href", "/tickets/11");
    expect(within(ticket).queryByText("Attention")).not.toBeInTheDocument();
  });

  it("affiche les étapes vides à la demande et mémorise le choix", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    await user.click(await screen.findByRole("button", { name: "Afficher les étapes vides (1)" }));
    const cols = screen.getAllByTestId("pipeline-column");
    expect(cols).toHaveLength(3);
    expect(within(cols[2]!).getByText("Vide")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Masquer les étapes vides" })).toHaveAttribute("aria-pressed", "true");
    unmount();
    renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    expect(await screen.findAllByTestId("pipeline-column")).toHaveLength(3);
  });

  it("n'offre pas de glisser-déposer", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    const cards = await screen.findAllByTestId("pipeline-card");
    for (const c of cards) expect(c).not.toHaveAttribute("draggable", "true");
  });

  it("affiche un état vide", async () => {
    mockFetch({ "GET /api/pipeline": { body: { columns: [{ state: "IDEA", label: "Idée", items: [] }] } } });
    renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    expect(await screen.findByText("Pipeline vide")).toBeInTheDocument();
  });

  it("affiche une erreur avec possibilité de réessayer", async () => {
    mockFetch({ "GET /api/pipeline": { status: 503, body: { error: { code: "db_unavailable", message: "Base de données indisponible." } } } });
    renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    expect(await screen.findByRole("alert")).toHaveTextContent("Base de données indisponible.");
    expect(screen.getByRole("button", { name: "Réessayer" })).toBeInTheDocument();
  });
});
