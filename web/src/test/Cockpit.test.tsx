import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { setCsrfToken } from "@/api/client";
import type { Activity, Approval, Pipeline, PipelineItem } from "@/api/types";
import { filterEntries, TopBar } from "@/components/layout/Cockpit";
import BoardPage, { buildBoard } from "@/pages/Board";
import { mockFetch, renderWithProviders } from "./utils";

const ticket = (id: number, state: string, over: Partial<PipelineItem> = {}): PipelineItem => ({
  type: "ticket",
  id,
  key: `fraiszen-F6-T${id}`,
  title: `fraiszen-F6-T${id} · Ticket ${id}`,
  project: "fraiszen",
  state: state as PipelineItem["state"],
  updated_at: "2026-11-08T18:00:00Z",
  attention: false,
  feature_id: 6,
  feature_title: "Alerte frais réels",
  ...over,
});
const feature = (id: number, state: string, over: Partial<PipelineItem> = {}): PipelineItem => ({
  type: "feature",
  id,
  title: `Feature ${id}`,
  project: "fraiszen",
  state: state as PipelineItem["state"],
  updated_at: "2026-11-08T18:00:00Z",
  attention: false,
  ...over,
});
const pipeline = (features: PipelineItem[], tickets: PipelineItem[]): Pipeline => ({
  features: { columns: [{ state: "SPEC", label: "x", items: features }] },
  tickets: { columns: [{ state: "DEV", label: "x", items: tickets }] },
  projects: [{ id: 1, slug: "fraiszen" }],
});
const subject = {
  type: "ticket" as const,
  id: 4,
  key: "fraiszen-F6-T4",
  title: "Récapitulatif",
  feature_id: 6,
  feature_title: "Alerte frais réels",
  project: "fraiszen",
};
const activity: Activity = {
  running: [
    {
      run_id: 90,
      role: "developer",
      job_kind: "develop",
      status: "running",
      total_tokens: 38000,
      started_at: "2026-11-08T18:00:00Z",
      last: {
        id: 0,
        run_id: 90,
        role: "developer",
        kind: "tool",
        tool: "terminal",
        text: "aos-ci phpunit --filter Summary",
        at: "2026-11-08T18:01:00Z",
        subject,
      },
      subject,
    },
  ],
  events: [
    { id: 12, run_id: 90, role: "developer", kind: "tool", tool: "terminal", text: "aos-ci phpunit --filter Summary", at: "2026-11-08T18:01:00Z", subject },
    {
      id: 11,
      run_id: 88,
      role: "qa",
      kind: "done",
      text: "a terminé son travail",
      at: "2026-11-08T17:50:00Z",
      subject: { ...subject, id: 3, key: "fraiszen-F6-T3" },
    },
  ],
};
const approval = (id: number, title: string): Approval => ({
  id,
  kind: "publish_pr",
  subject_type: "ticket",
  subject_id: 4,
  title,
  effect: "Créer une PR draft vers la branche de feature.",
  details: null,
  status: "pending",
  decided_by: null,
  decided_at: null,
  decision_comment: null,
  executed_at: null,
  execution_result: null,
  created_at: "2026-11-08T18:00:00Z",
});

describe("Tableau en direct", () => {
  it("répartit tickets et features par colonne et rattache le run en cours", () => {
    const b = buildBoard(
      [feature(7, "SPEC_REVIEW"), feature(8, "SPEC"), feature(6, "IN_DEV"), feature(9, "READY_FOR_DEV", { progress: { total: 2, done: 0, current: null } })],
      [ticket(4, "DEV"), ticket(5, "READY_FOR_DEV"), ticket(3, "AWAITING_PR_APPROVAL"), ticket(2, "QA")],
      activity.running,
    );
    expect(b.design?.map((c) => c.item.id)).toEqual([8]);
    expect(b.ready?.map((c) => c.item.id)).toEqual([5]);
    expect(b.dev?.[0]?.run?.run_id).toBe(90);
    expect(b.qa?.map((c) => c.item.id)).toEqual([2]);
    expect(b.you?.map((c) => `${c.item.type}-${c.item.id}`)).toEqual(["ticket-3", "feature-7"]);
  });

  it("affiche l'agent au travail sur sa carte, ta décision et le fil d'activité", async () => {
    mockFetch({
      "GET /api/pipeline": {
        body: pipeline([feature(7, "SPEC_REVIEW", { title: "Semaine type" })], [ticket(4, "DEV", { title: "fraiszen-F6-T4 · Récapitulatif" })]),
      },
      "GET /api/activity": { body: activity },
      "GET /api/overview": {
        body: {
          pending_approvals: 1,
          tokens_today: 412830,
          blocked: { features: 0, tickets: 0 },
          active_runs: 1,
          tokens_7d: [],
          dispatch_paused: false,
          alerts: [],
          recent_activity: [],
        },
      },
    });
    renderWithProviders(<BoardPage />);
    const dev = within(await screen.findByTestId("col-dev"));
    expect(await dev.findByText("Récapitulatif")).toBeInTheDocument();
    await waitFor(() => expect(dev.getByTestId("card-agent")).toBeInTheDocument());
    expect(dev.getByTestId("card-agent")).toHaveTextContent("Développeur au travail");
    expect(dev.getByTestId("card-agent")).toHaveTextContent("terminal · aos-ci phpunit --filter Summary");
    expect(dev.getByRole("link")).toHaveAttribute("href", "/tickets/4");
    const you = within(screen.getByTestId("col-you"));
    expect(you.getByText("Valider la spec")).toBeInTheDocument();
    expect(you.getByRole("link")).toHaveAttribute("href", "/features/7");
    const feed = within(screen.getByTestId("activity-feed"));
    expect(feed.getAllByRole("listitem")).toHaveLength(2);
    expect(feed.getByText("a terminé son travail")).toBeInTheDocument();
    expect(await screen.findByText("412 830 tokens aujourd'hui")).toBeInTheDocument();
  });
});

describe("À décider et Ctrl+K", () => {
  beforeEach(() => setCsrfToken("csrf"));

  it("filtre la palette par mots, sans accents ni casse", () => {
    const entries = [
      { id: "1", label: "F6 · Alerte frais réels", hint: "feature", to: "/features/6" },
      { id: "2", label: "Valider la spec de F7", hint: "à décider", to: "/approvals/3", keywords: "approuver" },
    ];
    expect(filterEntries(entries, "REELS f6").map((e) => e.id)).toEqual(["1"]);
    expect(filterEntries(entries, "approuver").map((e) => e.id)).toEqual(["2"]);
    expect(filterEntries(entries, "  ")).toHaveLength(2);
  });

  it("approuve depuis le tiroir et exige une raison pour refuser", async () => {
    const { calls } = mockFetch({
      "GET /api/approvals": { body: { items: [approval(3, "Publier la PR · F6-T4"), approval(5, "Valider la spec F7")], next_cursor: null } },
      "GET /api/pipeline": { body: pipeline([], []) },
      "POST /api/approvals/3/decision": { body: approval(3, "Publier la PR · F6-T4") },
      "POST /api/approvals/5/decision": { body: approval(5, "Valider la spec F7") },
    });
    const user = userEvent.setup();
    renderWithProviders(<TopBar />);
    const button = await screen.findByRole("button", { name: /À décider\s*2/ });
    await user.click(button);
    const drawer = within(screen.getByRole("dialog", { name: "À décider" }));
    await user.click(within(drawer.getByTestId("decision-3")).getByRole("button", { name: "Approuver" }));
    await waitFor(() => expect(calls.some((c) => c.url === "/api/approvals/3/decision")).toBe(true));
    expect(calls.find((c) => c.url === "/api/approvals/3/decision")!.body).toEqual({ decision: "approved", comment: "" });

    const second = within(drawer.getByTestId("decision-5"));
    await user.click(second.getByRole("button", { name: "Refuser" }));
    const confirm = second.getByRole("button", { name: "Confirmer le refus" });
    expect(confirm).toBeDisabled();
    await user.type(second.getByRole("textbox"), "Il manque le cas des jours fériés.");
    await user.click(confirm);
    await waitFor(() => expect(calls.some((c) => c.url === "/api/approvals/5/decision")).toBe(true));
    expect(calls.find((c) => c.url === "/api/approvals/5/decision")!.body).toEqual({ decision: "rejected", comment: "Il manque le cas des jours fériés." });
  });

  it("Ctrl+K ouvre la palette : décisions, features et tickets, navigation au clavier", async () => {
    mockFetch({
      "GET /api/approvals": { body: { items: [approval(3, "Publier la PR · F6-T4")], next_cursor: null } },
      "GET /api/pipeline": {
        body: pipeline([feature(6, "IN_DEV", { title: "Alerte frais réels" })], [ticket(4, "DEV", { title: "fraiszen-F6-T4 · Récapitulatif" })]),
      },
    });
    const user = userEvent.setup();
    renderWithProviders(<TopBar />);
    await screen.findByRole("button", { name: /À décider\s*1/ });
    fireEvent.keyDown(document, { key: "k", ctrlKey: true });
    const input = await screen.findByRole("combobox", { name: /Feature, ticket/ });
    await waitFor(() => expect(screen.getByRole("option", { name: /F6 · Alerte frais réels/ })).toBeInTheDocument());
    expect(screen.getByRole("option", { name: /Publier la PR · F6-T4/ })).toHaveTextContent("à décider");
    await user.type(input, "recap");
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    expect(options[0]).toHaveTextContent("fraiszen-F6-T4 · Récapitulatif");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
});
