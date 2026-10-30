import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { RecordingSession } from "@/api/types";
import { QARecordings } from "@/pages/QARecordings";
import { mockFetch, renderWithProviders } from "./utils";

const session = (over: Partial<RecordingSession> = {}): RecordingSession => ({
  run_id: 52,
  created_at: "2026-10-30T20:00:00Z",
  expires_at: "2026-11-14T20:00:00Z",
  report: {
    ok: false,
    error: "Timeout 15000ms exceeded",
    duration_ms: 9400,
    steps: [
      { name: "connexion", ok: true, ms: 1200 },
      { name: "récapitulatif", ok: false, ms: 8000, error: "locator.waitFor: Timeout" },
    ],
    console_errors: ["TypeError: total is undefined"],
    failed_requests: ["500 GET /api/summary"],
    blocked_external: 2,
  },
  video: { id: 1, name: "video.webm", size: 340_000 },
  trace: { id: 2, name: "trace.zip", size: 1_044_282 },
  scenario: { id: 3, name: "scenario.mjs", size: 300 },
  shots: [{ id: 4, name: "shots/01-recapitulatif.png", size: 172_000, label: "Récapitulatif 2025", criterion: 2 }],
  logs: [{ id: 5, name: "logs/api.log", size: 1800 }],
  ...over,
});

describe("Onglet Enregistrement QA", () => {
  it("affiche vidéo, captures liées aux critères, problèmes relevés et lien vers la trace", async () => {
    mockFetch({ "GET /api/tickets/9/recordings": { body: { items: [session()], next_cursor: null } } });
    renderWithProviders(<QARecordings ticketId={9} acceptance={["Le calendrier s'affiche", "Le récapitulatif montre le forfait 10 %"]} />);
    const s = within(await screen.findByTestId("recording-52"));
    expect(s.getByText("Scénario en échec")).toBeInTheDocument();
    expect(s.getByLabelText("Vidéo de la session QA")).toHaveAttribute("src", "/api/recordings/1/file");
    expect(s.getByRole("img", { name: "Récapitulatif 2025" })).toHaveAttribute("src", "/api/recordings/4/file");
    expect(s.getByText("Critère 2")).toBeInTheDocument();
    expect(s.getByText(/Le récapitulatif montre le forfait 10 %/)).toBeInTheDocument();
    const trace = s.getByRole("link", { name: /Ouvrir la trace/ });
    expect(trace).toHaveAttribute("href", "/trace-viewer/index.html?trace=%2Fapi%2Frecordings%2F2%2Ffile");
    expect(trace).toHaveAttribute("target", "_blank");
    const issues = s.getByTestId("recording-issues");
    expect(issues).toHaveTextContent("4 problème(s) relevé(s)");
    expect(issues).toHaveTextContent("Requête : 500 GET /api/summary");
    expect(issues).toHaveTextContent("Erreur JS : TypeError: total is undefined");
    expect(s.getByText("2 requête(s) externe(s) bloquée(s) (sans réseau)", { exact: false })).toBeInTheDocument();
  });

  it("session réussie sans problème, puis état vide", async () => {
    const ok = session({
      report: {
        ok: true,
        error: null,
        duration_ms: 7000,
        steps: [{ name: "connexion", ok: true, ms: 900 }],
        console_errors: [],
        failed_requests: [],
        blocked_external: 0,
      },
    });
    mockFetch({ "GET /api/tickets/9/recordings": { body: { items: [ok], next_cursor: null } } });
    const { unmount } = renderWithProviders(<QARecordings ticketId={9} acceptance={[]} />);
    expect(await screen.findByText("Scénario réussi")).toBeInTheDocument();
    expect(screen.queryByTestId("recording-issues")).not.toBeInTheDocument();
    unmount();
    mockFetch({ "GET /api/tickets/9/recordings": { body: { items: [], next_cursor: null } } });
    renderWithProviders(<QARecordings ticketId={9} acceptance={[]} />);
    expect(await screen.findByText("Aucun enregistrement QA")).toBeInTheDocument();
  });
});
