import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { applyInvalidation, backoffDelay, INVALIDATION_MAP } from "@/api/events";
import { SSE_EVENT_TYPES } from "@/api/types";
import { diffKeys } from "@/components/domain";
import { normalizeHealth } from "@/pages/Health";
import { validateProject } from "@/pages/Projects";

describe("SSE", () => {
  it("chaque type d'événement du contrat a une règle d'invalidation", () => {
    for (const t of SSE_EVENT_TYPES) expect(INVALIDATION_MAP[t]).toBeDefined();
    expect(INVALIDATION_MAP["approval.updated"]).toContain("approval");
    expect(INVALIDATION_MAP["overview.invalidate"]).toEqual(["overview"]);
  });

  it("invalide les requêtes ciblées", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyInvalidation(qc, "feature.updated");
    const keys = spy.mock.calls.map((c) => (c[0] as { queryKey: string[] }).queryKey[0]);
    expect(keys).toEqual(expect.arrayContaining(["feature", "features", "pipeline", "overview"]));
  });

  it("backoff exponentiel plafonné à ~30 s", () => {
    expect(backoffDelay(0)).toBeGreaterThanOrEqual(800);
    expect(backoffDelay(0)).toBeLessThanOrEqual(1200);
    expect(backoffDelay(3)).toBeGreaterThanOrEqual(6400);
    expect(backoffDelay(20)).toBeLessThanOrEqual(36000);
  });
});

describe("utilitaires d'écran", () => {
  it("diff before/after de premier niveau", () => {
    expect(diffKeys({ state: "SPEC", v: 1, same: true }, { state: "SPEC_REVIEW", v: 2, same: true })).toEqual([
      { key: "state", before: "SPEC", after: "SPEC_REVIEW" },
      { key: "v", before: 1, after: 2 },
    ]);
    expect(diffKeys(null, null)).toEqual([]);
  });

  it("normalise les composants de santé", () => {
    expect(normalizeHealth("ok")).toEqual({ status: "ok" });
    expect(normalizeHealth(false)).toEqual({ status: "error" });
    expect(normalizeHealth({ status: "degraded", message: "lent" })).toEqual({ status: "degraded", message: "lent" });
    expect(normalizeHealth({ ok: true })).toEqual({ status: "ok", message: undefined });
  });

  it("valide le formulaire projet", () => {
    const ok = { slug: "shop", name: "Shop", repository: "acme/shop", base_branch: "main", required_checks: "", mode: "sandbox" as const, test_command: "" };
    expect(validateProject(ok, true)).toEqual({});
    expect(validateProject({ ...ok, slug: "Shop!", repository: "acme" }, true)).toMatchObject({ slug: expect.any(String), repository: expect.any(String) });
  });
});
