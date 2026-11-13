import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setCsrfToken } from "@/api/client";
import type { FeatureThread as Thread } from "@/api/types";
import { FeatureThread, threadItems } from "@/components/FeatureThread";
import { mockFetch, renderWithProviders } from "./utils";

const thread = (over: Partial<Thread> = {}): Thread => ({
  feature_id: 7,
  closed: false,
  working: "developer",
  sessions: { po: true, developer: true },
  runs: [
    {
      id: 11,
      agent_role: "po",
      job_kind: "po_spec",
      ticket_key: null,
      status: "completed",
      summary: "Spec : semaine type persistée.",
      error: null,
      fresh_tokens: 2500,
      total_tokens: 18000,
      model: "m",
      started_at: "2026-11-13T09:00:00Z",
      finished_at: "2026-11-13T09:02:00Z",
    },
    {
      id: 12,
      agent_role: "developer",
      job_kind: "develop",
      ticket_key: "fz-F7-T1",
      status: "running",
      summary: "",
      error: null,
      fresh_tokens: 0,
      total_tokens: 0,
      model: null,
      started_at: "2026-11-13T10:00:00Z",
      finished_at: null,
    },
    {
      id: 13,
      agent_role: "po",
      job_kind: "answer_question",
      ticket_key: null,
      status: "completed",
      summary: "",
      error: null,
      fresh_tokens: 3100,
      total_tokens: 9000,
      model: "m",
      started_at: "2026-11-13T10:05:00Z",
      finished_at: "2026-11-13T10:06:00Z",
    },
  ],
  approvals: [
    {
      id: 5,
      kind: "spec_review",
      subject_type: "feature",
      subject_id: 7,
      title: "Spécification : Semaine type",
      effect: "",
      details: null,
      status: "executed",
      decision: "approved",
      decided_by: "lilian",
      decided_at: "2026-11-13T09:30:00Z",
      decision_comment: null,
      executed_at: null,
      execution_result: null,
      created_at: "2026-11-13T09:03:00Z",
    } as Thread["approvals"][number],
  ],
  messages: [
    {
      id: 1,
      kind: "question",
      author: "user:lilian",
      target_role: "po",
      body: "Pourquoi 5 jours ?",
      status: "answered",
      reply_to: null,
      run_id: 13,
      note: null,
      created_at: "2026-11-13T10:04:00Z",
    },
    {
      id: 2,
      kind: "answer",
      author: "agent:po",
      target_role: "po",
      body: "La spec suit la semaine de travail standard.",
      status: "answered",
      reply_to: 1,
      run_id: 13,
      note: null,
      created_at: "2026-11-13T10:06:00Z",
    },
    {
      id: 3,
      kind: "instruction",
      author: "user:lilian",
      target_role: null,
      body: "Pense aux jours fériés.",
      status: "delivered",
      reply_to: null,
      run_id: 12,
      note: null,
      created_at: "2026-11-13T10:07:00Z",
    },
  ],
  ...over,
});

describe("Fil d'équipe", () => {
  beforeEach(() => setCsrfToken("csrf"));

  it("ordonne passages, décisions et messages, sans dupliquer les runs de réponse", () => {
    const kinds = threadItems(thread()).map((i) => (i.type === "run" ? `run:${i.run.id}` : i.type === "approval" ? "approval" : `msg:${i.message.id}`));
    expect(kinds).toEqual(["run:11", "approval", "run:12", "msg:1", "msg:2", "msg:3"]);
  });

  it("affiche le fil et propose de transformer une réponse en correction", async () => {
    mockFetch({ "GET /api/features/7/thread": { body: thread() } });
    const onCorrect = vi.fn();
    renderWithProviders(<FeatureThread featureId={7} onCorrect={onCorrect} />);
    expect(await screen.findByText("Spec : semaine type persistée.")).toBeInTheDocument();
    expect(screen.getByTestId("thread-run-12")).toHaveTextContent("Au travail");
    expect(screen.getByTestId("thread-approval-5")).toHaveTextContent("approuvée par lilian");
    const answer = screen.getByTestId("thread-answer-2");
    expect(answer).toHaveTextContent("La spec suit la semaine de travail standard.");
    expect(answer).toHaveTextContent("3 100 tokens");
    expect(screen.getByTestId("thread-instruction-3")).toHaveTextContent("transmise à l'agent en cours");
    await userEvent.click(within(answer).getByRole("button", { name: "Transformer en correction" }));
    expect(onCorrect).toHaveBeenCalledWith({
      title: "Pourquoi 5 jours ?",
      description: expect.stringContaining("La spec suit la semaine de travail standard."),
    });
  });

  it("envoie une consigne à l'agent en cours, puis une question au PO", async () => {
    const { calls } = mockFetch({
      "GET /api/features/7/thread": { body: thread() },
      "POST /api/features/7/messages": (c) => ({
        status: 201,
        body: { id: 9, kind: (c.body as { kind: string }).kind, status: (c.body as { kind: string }).kind === "question" ? "answering" : "delivered" },
      }),
    });
    renderWithProviders(<FeatureThread featureId={7} />);
    const composer = await screen.findByTestId("composer");
    expect(within(composer).getByLabelText("Destinataire")).toHaveDisplayValue("Agent en cours (Développeur)");
    expect(screen.getByTestId("composer-hint")).toHaveTextContent("transmise tout de suite");
    await userEvent.type(within(composer).getByLabelText("Message"), "Ajoute un test.");
    await userEvent.click(within(composer).getByRole("button", { name: "Envoyer" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ kind: "instruction", role: "", body: "Ajoute un test." });
    expect(await screen.findByText("Consigne transmise")).toBeInTheDocument();

    // Question : le développeur travaille → envoi bloqué ; au PO → reprise de sa session.
    await userEvent.click(within(composer).getByRole("button", { name: "Question" }));
    await userEvent.type(within(composer).getByLabelText("Message"), "Pourquoi ?");
    expect(within(composer).getByRole("button", { name: "Envoyer" })).toBeDisabled();
    expect(screen.getByTestId("composer-hint")).toHaveTextContent("envoie-lui plutôt une consigne");
    await userEvent.selectOptions(within(composer).getByLabelText("Destinataire"), "po");
    expect(screen.getByTestId("composer-hint")).toHaveTextContent("dans sa session sur la feature");
    await userEvent.click(within(composer).getByRole("button", { name: "Envoyer" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2));
    expect(calls.filter((c) => c.method === "POST")[1]!.body).toEqual({ kind: "question", role: "po", body: "Pourquoi ?" });
  });

  it("ferme la discussion sur une feature terminée", async () => {
    mockFetch({ "GET /api/features/7/thread": { body: thread({ closed: true, working: null }) } });
    renderWithProviders(<FeatureThread featureId={7} />);
    expect(await screen.findByText(/la discussion est close/)).toBeInTheDocument();
    expect(screen.queryByTestId("composer")).not.toBeInTheDocument();
  });
});
