import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
  it("rend les colonnes et cartes à partir de la réponse API", async () => {
    mockFetch({ "GET /api/pipeline": { body: pipeline } });
    renderWithProviders(<PipelinePage />, { route: "/pipeline", path: "/pipeline" });
    const cols = await screen.findAllByTestId("pipeline-column");
    expect(cols).toHaveLength(3);
    expect(within(cols[0]!).getByRole("heading", { name: "Revue spec" })).toBeInTheDocument();
    expect(within(cols[1]!).getAllByTestId("pipeline-card")).toHaveLength(2);
    expect(within(cols[2]!).getByText("Vide")).toBeInTheDocument();

    const feature = screen.getByText("Export CSV").closest("a")!;
    expect(feature).toHaveAttribute("href", "/features/3");
    expect(within(feature).getByText("Attention")).toBeInTheDocument();
    const ticket = screen.getByText("Endpoint export").closest("a")!;
    expect(ticket).toHaveAttribute("href", "/tickets/11");
    expect(within(ticket).queryByText("Attention")).not.toBeInTheDocument();
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
