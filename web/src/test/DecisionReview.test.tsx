import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import type { Approval } from "@/api/types";
import { composeFeedback, composeRework, emptyDraft, findingItems, parseQuestions } from "@/lib/decision";
import ApprovalDetailPage from "@/pages/ApprovalDetail";
import { mockFetch, renderWithProviders } from "./utils";

type Body = {
  decision?: string;
  comment: string;
  kind?: string;
  role?: string;
  body: string;
};
const bodyOf = (c: { body?: unknown } | undefined) => c?.body as Body;

const spec: Approval = {
  id: 27,
  kind: "spec_review",
  subject_type: "feature",
  subject_id: 7,
  title: "Spec : Semaine type",
  effect: "Valider la spec et lancer la conception.",
  details: {
    spec: {
      summary: "Décrire une semaine type.",
      user_stories: ["En tant que salarié…"],
      acceptance_criteria: ["Les fériés sont ignorés.", "Aucun doublon."],
      open_questions: [],
    },
    open_questions: [
      {
        question: "Week-end ?",
        options: ["Lundi → vendredi", "Honorer le week-end"],
        recommended: 0,
      },
      {
        question: "Favori supprimé ?",
        options: ["Ignorer le jour", "Tout bloquer"],
        recommended: 0,
      },
      "Jours fériés régionaux ?",
    ],
  },
  status: "pending",
  decided_by: null,
  decided_at: null,
  decision_comment: null,
  executed_at: null,
  execution_result: null,
  created_at: "2026-10-30T10:00:00Z",
};

const renderPage = (id: number) =>
  renderWithProviders(<ApprovalDetailPage />, {
    route: `/approvals/${id}`,
    path: "/approvals/:id",
  });

beforeEach(() => localStorage.clear());

describe("Logique des décisions", () => {
  it("accepte l'ancien et le nouveau format de questions", () => {
    const qs = parseQuestions(["a ?", { question: "b ?", options: ["x", "y"], recommended: 1 }, { question: "c ?", options: ["x"], recommended: 4 }, 3]);
    expect(qs).toEqual([
      { question: "a ?", options: [], recommended: null },
      { question: "b ?", options: ["x", "y"], recommended: 1 },
      { question: "c ?", options: ["x"], recommended: null },
    ]);
  });

  it("compose un retour question → réponse sans en oublier", () => {
    const qs = parseQuestions([{ question: "Q1", options: ["A", "B"] }, "Q2", "Q3"]);
    const d = {
      ...emptyDraft(),
      answers: {
        0: { choice: 1, text: "mais court", delegate: false },
        1: { choice: null, text: "", delegate: true },
      },
      notes: { c0: "trop vague" },
      remark: "Merci",
    };
    const out = composeFeedback(qs, d, "PO", [{ key: "c0", label: "critère 1" }]);
    expect(out).toContain("1. Q1\n   → B — mais court");
    expect(out).toContain("2. Q2\n   → laissé à ta décision (PO).");
    expect(out).toContain("3. Q3\n   → sans réponse");
    expect(out).toContain("- critère 1 : trop vague");
    expect(out).toContain("Remarque générale : Merci");
  });

  it("liste les constats écartés d'une reprise", () => {
    const items = findingItems({
      findings: "- [P1] a.php : bug\n- [P3] b.php : nom\n",
    });
    expect(items).toEqual(["- [P1] a.php : bug", "- [P3] b.php : nom"]);
    expect(
      composeRework(items, {
        ...emptyDraft(),
        dropped: [1],
        remark: "minimal",
      }),
    ).toBe("Constats écartés par l'humain (ne pas les traiter) :\n- ✗ [P3] b.php : nom\n\nminimal");
  });
});

describe("Revue de spec", () => {
  it("répond en un clic, laisse une question au PO, écrit librement et envoie un retour structuré", async () => {
    const { calls } = mockFetch({
      "GET /api/approvals/27": { body: spec },
      "POST /api/approvals/27/decision": { body: { ok: true } },
    });
    renderPage(27);
    const user = userEvent.setup();
    const cards = await screen.findAllByTestId("question-card");
    expect(cards).toHaveLength(3);
    expect(within(cards[0]!).getByRole("button", { name: /Lundi → vendredi/ })).toHaveTextContent("recommandé");
    expect(screen.getByRole("button", { name: "Envoyer mes réponses au PO" })).toBeDisabled();

    await user.click(within(cards[0]!).getByRole("button", { name: /Honorer le week-end/ }));
    await user.click(within(cards[1]!).getByRole("button", { name: "Laisser le PO décider" }));
    await user.type(within(cards[2]!).getByLabelText("Ta réponse à la question 3"), "Hors périmètre");
    expect(screen.getByTestId("answered-count")).toHaveTextContent("3 / 3");
    expect(localStorage.getItem("aos-approval-draft-27")).toContain("Hors périmètre");

    await user.click(screen.getByRole("button", { name: "Envoyer mes réponses au PO" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = { body: bodyOf(calls.find((c) => c.method === "POST")) };
    expect(post.body.decision).toBe("rejected");
    expect(post.body.comment).toContain("1. Week-end ?\n   → Honorer le week-end");
    expect(post.body.comment).toContain("2. Favori supprimé ?\n   → laissé à ta décision (PO).");
    expect(post.body.comment).toContain("3. Jours fériés régionaux ?\n   → Hors périmètre");
  });

  it("prévient avant d'approuver avec des questions sans réponse", async () => {
    const { calls } = mockFetch({
      "GET /api/approvals/27": { body: spec },
      "POST /api/approvals/27/decision": { body: { ok: true } },
    });
    renderPage(27);
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", {
        name: "Approuver et lancer la conception",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("3 questions restent sans réponse");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "Approuver quand même" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "POST")?.body).toEqual({
        decision: "approved",
        comment: "",
      }),
    );
  });

  it("commente un critère précis", async () => {
    const { calls } = mockFetch({
      "GET /api/approvals/27": { body: spec },
      "POST /api/approvals/27/decision": { body: { ok: true } },
    });
    renderPage(27);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Commenter : critère 2" }));
    await user.type(screen.getByLabelText("Ta remarque : critère 2"), "Préciser le double-clic");
    await user.click(screen.getByRole("button", { name: "Envoyer mes réponses au PO" }));
    await waitFor(() =>
      expect(bodyOf(calls.find((c) => c.method === "POST"))?.comment).toContain("- critère 2 (« Aucun doublon. ») : Préciser le double-clic"),
    );
  });

  it("demande au PO et affiche sa réponse sous la question", async () => {
    const { calls } = mockFetch({
      "GET /api/approvals/27": { body: spec },
      "POST /api/features/7/messages": {
        body: {
          id: 91,
          kind: "question",
          author: "user:admin",
          target_role: "po",
          body: "…",
          status: "answering",
          reply_to: null,
          run_id: null,
          note: null,
          created_at: "2026-11-14T10:00:00Z",
        },
      },
      "GET /api/features/7/thread": {
        body: {
          feature_id: 7,
          closed: false,
          working: null,
          sessions: { po: true },
          runs: [],
          approvals: [],
          messages: [
            {
              id: 92,
              kind: "answer",
              author: "agent:po",
              target_role: null,
              body: "Je recommande lundi → vendredi.",
              status: "answered",
              reply_to: 91,
              run_id: 5,
              note: null,
              created_at: "2026-11-14T10:00:20Z",
            },
          ],
        },
      },
    });
    renderPage(27);
    const user = userEvent.setup();
    const cards = await screen.findAllByTestId("question-card");
    await user.click(within(cards[0]!).getByRole("button", { name: "Demander au PO" }));
    const reply = await within(cards[0]!).findByTestId("agent-reply");
    await waitFor(() => expect(reply).toHaveTextContent("Je recommande lundi → vendredi."));
    const post = {
      body: bodyOf(calls.find((c) => c.url === "/api/features/7/messages")),
    };
    expect(post.body).toMatchObject({ kind: "question", role: "po" });
    expect(post.body.body).toContain("À propos de ta question 1 (« Week-end ? »)");
    await user.click(
      within(cards[0]!).getByRole("button", {
        name: "Reprendre sa recommandation",
      }),
    );
    expect(within(cards[0]!).getByRole("button", { name: /Lundi → vendredi/ })).toHaveAttribute("aria-pressed", "true");
  });
});

describe("Reprise après revue", () => {
  const rework: Approval = {
    ...spec,
    id: 40,
    kind: "rework",
    subject_type: "ticket",
    subject_id: 12,
    title: "Rework : Forfait",
    effect: "Relancer le développement (tentative 2/3).",
    details: {
      source: "review",
      summary: "Condition fausse.",
      finding_items: ["- [P1] a.php : condition\n", "- [P3] a.php : renommer\n"],
      findings: "x",
      sha: "9c41e2a",
      attempt: 1,
      max_rework: 3,
    },
  };

  it("ne garde que les constats choisis", async () => {
    const { calls } = mockFetch({
      "GET /api/approvals/40": { body: rework },
      "GET /api/tickets/12": {
        body: {
          id: 12,
          feature_id: 6,
          project_id: 1,
          key: "fz-F6-T4",
          title: "Forfait",
          body: "",
          state: "CHANGES_REQUESTED",
          attempt: 1,
          version: 1,
          created_at: "",
          updated_at: "",
        },
      },
      "POST /api/approvals/40/decision": { body: { ok: true } },
    });
    renderPage(40);
    const user = userEvent.setup();
    const findings = await screen.findAllByTestId("finding");
    await user.click(within(findings[1]!).getByRole("button", { name: "Pas d'accord" }));
    await user.click(
      screen.getByRole("button", {
        name: "Renvoyer au développeur (tentative 2 / 3)",
      }),
    );
    await waitFor(() =>
      expect(calls.find((c) => c.method === "POST")?.body).toEqual({
        decision: "approved",
        comment: "Constats écartés par l'humain (ne pas les traiter) :\n- ✗ [P3] a.php : renommer",
      }),
    );
  });
});
