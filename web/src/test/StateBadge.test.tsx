import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StateBadge } from "@/components/ui/StateBadge";
import { FEATURE_STATES, TICKET_STATES, RUN_STATUSES, APPROVAL_STATUSES } from "@/api/types";
import { STATE_META, stateMeta } from "@/lib/states";

describe("StateBadge", () => {
  it("affiche le libellé français et la tonalité de l'état", () => {
    render(<StateBadge state="SPEC_REVIEW" />);
    const el = screen.getByText("Revue spec").closest("[data-state]")!;
    expect(el).toHaveAttribute("data-state", "SPEC_REVIEW");
    expect(el).toHaveAttribute("data-tone", "review");
  });

  it("utilise une palette cohérente : bloqué = danger, terminé = succès", () => {
    const { rerender } = render(<StateBadge state="BLOCKED" />);
    expect(screen.getByText("Bloqué").closest("[data-tone]")).toHaveAttribute("data-tone", "danger");
    rerender(<StateBadge state="DONE" />);
    expect(screen.getByText("Terminé").closest("[data-tone]")).toHaveAttribute("data-tone", "success");
    rerender(<StateBadge state="execution_failed" />);
    expect(screen.getByText("Échec d'exécution").closest("[data-tone]")).toHaveAttribute("data-tone", "danger");
  });

  it("retombe sur l'identifiant brut pour un état inconnu", () => {
    render(<StateBadge state="WEIRD_STATE" />);
    expect(screen.getByText("WEIRD_STATE").closest("[data-tone]")).toHaveAttribute("data-tone", "neutral");
  });

  it("couvre tous les états du contrat", () => {
    for (const s of [...FEATURE_STATES, ...TICKET_STATES, ...RUN_STATUSES, ...APPROVAL_STATUSES, "unverified", "pending", "success", "failure", "SYNCED", "DRIFT_DETECTED", "UNKNOWN", "UNREACHABLE"]) {
      expect(STATE_META[s], s).toBeDefined();
      expect(stateMeta(s).label.length).toBeGreaterThan(0);
    }
  });
});
