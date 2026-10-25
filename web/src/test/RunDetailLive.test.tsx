import { act, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventStreamProvider } from "@/api/events";
import { fetchAllPages } from "@/api/queries";
import RunDetailPage from "@/pages/RunDetail";
import { mockFetch, renderWithProviders } from "./utils";

/** EventSource factice : permet d'émettre des événements et de simuler une coupure. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  private listeners = new Map<string, ((ev: MessageEvent<string>) => void)[]>();
  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: (ev: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
  static last() {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1]!;
  }
}

const runBody = (extra: Record<string, unknown> = {}) => ({
  run: {
    id: 5, job_id: 9, agent_role: "developer", hermes_profile: "aos-developer", hermes_run_id: "r_1", status: "running",
    input_tokens: 0, output_tokens: 0, total_tokens: 0, model: null, provider: null, error: null,
    started_at: "2026-10-21T10:00:00Z", finished_at: null, stop_attempts: 0, stop_error: null, has_artifact: false, artifact: null,
  },
  job: { id: 9, kind: "develop" },
  events: [{ run_id: 5, seq: 1, event: "tool.started", data: { tool: "terminal" }, created_at: "2026-10-21T10:00:05Z" }],
  output: "",
  ...extra,
});

const renderRun = () =>
  renderWithProviders(
    <EventStreamProvider enabled>
      <RunDetailPage />
    </EventStreamProvider>,
    { route: "/runs/5", path: "/runs/:id" },
  );

const eventRows = () => within(screen.getByRole("list", { hidden: true })).getAllByRole("listitem");

describe("Détail d'un run : flux en direct", () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal("EventSource", FakeEventSource);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("affiche l'heure Europe/Paris des événements persistés et fusionne le direct sans doublon", async () => {
    mockFetch({ "GET /api/runs/5": { body: runBody() } });
    renderRun();
    await screen.findByText("tool.started");
    expect(screen.getByText("12:00:05")).toBeInTheDocument(); // 10:00:05Z = 12:00:05 à Paris (CEST)
    const es = FakeEventSource.last();
    act(() => { es.onopen?.(); });
    // doublon du seq déjà persisté : ignoré ; événement d'un autre run : ignoré ; nouveau seq : ajouté
    act(() => { es.emit("run.event", { id: 5, run_id: 5, seq: 1, event: "tool.started", data: {}, created_at: "2026-10-21T10:00:05Z" }); });
    act(() => { es.emit("run.event", { id: 6, run_id: 6, seq: 2, event: "autre.run", data: {}, created_at: "2026-10-21T10:00:06Z" }); });
    act(() => { es.emit("run.event", { id: 5, run_id: 5, seq: 2, event: "tool.completed", data: { ok: true }, created_at: "2026-10-21T10:00:07Z" }); });
    act(() => { es.emit("run.event", { id: 5, run_id: 5, seq: 2, event: "tool.completed", data: { ok: true }, created_at: "2026-10-21T10:00:07Z" }); });
    await screen.findByText("tool.completed");
    expect(screen.queryByText("autre.run")).not.toBeInTheDocument();
    expect(eventRows()).toHaveLength(2);
    expect(screen.getByText("12:00:07")).toBeInTheDocument();
  });

  it("après une reconnexion, relit le run pour récupérer les événements manqués", async () => {
    let gets = 0;
    mockFetch({
      "GET /api/runs/5": () => {
        gets++;
        return gets === 1
          ? { body: runBody() }
          : { body: runBody({ events: [...runBody().events, { run_id: 5, seq: 3, event: "run.completed", data: {}, created_at: "2026-10-21T10:01:00Z" }] }) };
      },
    });
    renderRun();
    await screen.findByText("tool.started");
    const first = FakeEventSource.last();
    act(() => { first.onopen?.(); });
    act(() => { first.onerror?.(); }); // coupure : l'événement seq 3 est émis pendant ce temps
    expect(first.closed).toBe(true);
    await waitFor(() => expect(FakeEventSource.instances.length).toBe(2), { timeout: 3000 });
    act(() => { FakeEventSource.last().onopen?.(); }); // reconnexion → invalidation → relecture
    await screen.findByText("run.completed");
    expect(gets).toBeGreaterThanOrEqual(2);
    expect(eventRows()).toHaveLength(2);
  });

  it("signale explicitement un historique d'événements tronqué", async () => {
    mockFetch({ "GET /api/runs/5": { body: runBody({ events_truncated: true }) } });
    renderRun();
    expect(await screen.findByTestId("truncated-notice")).toHaveTextContent("2 000 événements");
  });
});

describe("listes paginées", () => {
  it("lit toutes les pages au lieu de tronquer à 200", async () => {
    const { calls } = mockFetch({
      "GET /api/projects": (c) =>
        c.url.includes("cursor=")
          ? { body: { items: [{ id: 1 }], next_cursor: null } }
          : { body: { items: [{ id: 3 }, { id: 2 }], next_cursor: "2" } },
    });
    const all = await fetchAllPages<{ id: number }>("/api/projects", {});
    expect(all.map((p) => p.id)).toEqual([3, 2, 1]);
    expect(calls).toHaveLength(2);
    expect(calls[1]!.url).toContain("cursor=2");
  });

  it("refuse de masquer des éléments au-delà de la borne de sécurité", async () => {
    mockFetch({ "GET /api/approvals": { body: { items: [{ id: 1 }], next_cursor: "1" } } });
    await expect(fetchAllPages("/api/approvals", {}, undefined, 3)).rejects.toThrow("trop longue");
  });
});
