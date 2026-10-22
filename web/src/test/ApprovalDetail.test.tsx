import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { setCsrfToken } from "@/api/client";
import type { Approval } from "@/api/types";
import ApprovalDetailPage from "@/pages/ApprovalDetail";
import { mockFetch, renderWithProviders } from "./utils";

const base: Approval = {
  id: 7,
  kind: "design_review",
  subject_type: "feature",
  subject_id: 3,
  title: "Valider le design de « Export CSV »",
  effect: "Créer 3 issues sur acme/shop",
  details: { open_questions: ["Faut-il paginer ?"], tickets: ["Endpoint", "UI", "Tests"] },
  status: "pending",
  decided_by: null,
  decided_at: null,
  decision_comment: null,
  executed_at: null,
  execution_result: null,
  created_at: "2026-10-21T10:00:00Z",
};

const renderPage = () => renderWithProviders(<ApprovalDetailPage />, { route: "/approvals/7", path: "/approvals/:id" });

describe("Écran Approbation", () => {
  it("met en évidence ce que l'approbation autorise", async () => {
    mockFetch({ "GET /api/approvals/7": { body: base } });
    renderPage();
    const effect = await screen.findByTestId("approval-effect");
    expect(within(effect).getByText("Ce que cette approbation autorise")).toBeInTheDocument();
    expect(within(effect).getByText("Créer 3 issues sur acme/shop")).toBeInTheDocument();
    expect(screen.getByText("Faut-il paginer ?")).toBeInTheDocument();
  });

  it("bloque le rejet sans commentaire (aucun POST envoyé)", async () => {
    const { calls } = mockFetch({ "GET /api/approvals/7": { body: base } });
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Rejeter" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Un commentaire est obligatoire pour rejeter.");
    expect(screen.getByLabelText("Commentaire")).toHaveAttribute("aria-invalid", "true");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("rejette avec commentaire puis relit l'état depuis l'API", async () => {
    setCsrfToken("csrf-xyz");
    const { calls } = mockFetch({
      "GET /api/approvals/7": (_c, n) =>
        n === 1
          ? { body: base }
          : { body: { ...base, status: "rejected", decided_by: "admin", decided_at: "2026-10-21T10:05:00Z", decision_comment: "Découper autrement" } },
      "POST /api/approvals/7/decision": { body: { ok: true } },
    });
    renderPage();
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Commentaire"), "Découper autrement");
    await user.click(screen.getByRole("button", { name: "Rejeter" }));
    const outcome = await screen.findByTestId("approval-outcome");
    expect(outcome).toHaveAttribute("data-status", "rejected");
    expect(outcome).toHaveTextContent("Décision enregistrée — rejetée");
    const post = calls.find((c) => c.method === "POST")!;
    expect(post.body).toEqual({ decision: "rejected", comment: "Découper autrement" });
    expect(post.headers["X-CSRF-Token"]).toBe("csrf-xyz");
    expect(calls.filter((c) => c.method === "GET" && c.url === "/api/approvals/7").length).toBeGreaterThanOrEqual(2);
  });

  it("après approbation, distingue « décision enregistrée » puis « exécutée »", async () => {
    let gets = 0;
    mockFetch({
      "GET /api/approvals/7": () => {
        gets++;
        if (gets === 1) return { body: base };
        if (gets === 2) return { body: { ...base, status: "approved", decided_by: "admin", decided_at: "2026-10-21T10:05:00Z" } };
        return { body: { ...base, status: "executed", decided_by: "admin", decided_at: "2026-10-21T10:05:00Z", executed_at: "2026-10-21T10:05:30Z", execution_result: { issues: [12, 13, 14] } } };
      },
      "POST /api/approvals/7/decision": { body: { ok: true } },
    });
    const { client } = renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Approuver" }));
    const recorded = await screen.findByTestId("approval-outcome");
    expect(recorded).toHaveAttribute("data-status", "approved");
    expect(recorded).toHaveTextContent("Décision enregistrée — exécution en cours");

    // Relecture suivante (polling / SSE) : exécutée.
    await client.invalidateQueries({ queryKey: ["approval", 7] });
    await waitFor(() => expect(screen.getByTestId("approval-outcome")).toHaveAttribute("data-status", "executed"));
    expect(screen.getByTestId("approval-outcome")).toHaveTextContent("Exécutée");
  });

  it("affiche l'échec d'exécution", async () => {
    mockFetch({
      "GET /api/approvals/7": { body: { ...base, status: "execution_failed", decided_by: "admin", execution_result: { error: "GitHub 502" } } },
    });
    renderPage();
    const outcome = await screen.findByTestId("approval-outcome");
    expect(outcome).toHaveTextContent("Échec d'exécution");
    expect(outcome).toHaveTextContent("GitHub 502");
    expect(screen.queryByRole("button", { name: "Approuver" })).not.toBeInTheDocument();
  });

  it("sur 409 (déjà décidée), recharge l'état réel", async () => {
    let gets = 0;
    mockFetch({
      "GET /api/approvals/7": () => (++gets === 1 ? { body: base } : { body: { ...base, status: "executed", decided_by: "autre" } }),
      "POST /api/approvals/7/decision": { status: 409, body: { error: { code: "already_decided", message: "Approbation déjà décidée." } } },
    });
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Approuver" }));
    await waitFor(() => expect(screen.getByTestId("approval-outcome")).toHaveAttribute("data-status", "executed"));
  });
});
